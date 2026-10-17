package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	compactClassRE   = regexp.MustCompile(`\bclass\s+([\w$]+)\s+extends\s+([\w$.]+)\s*\{`)
	compactMethodRE  = regexp.MustCompile(`\bstatic\s+\$\s*\(\s*\)\s*\{\s*return\s*`)
	compactPackageRE = regexp.MustCompile(`\bstatic\s+\$p\s*\(\s*\)\s*\{\s*return\s*([^;}]+)`)
	compactConstRE   = regexp.MustCompile(`(?:\b(?:var|let|const)\s+|,)\s*([\w$]+)\s*=\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`)
	compactEnumRE    = regexp.MustCompile(`(?:\b(?:var|let|const)\s+|[,;])\s*([\w$]+)\s*=\s*(?:\(\s*(?:[\w$.]+\.proto3\s*,\s*)+)*\(\s*0\s*,\s*([\w$]+(?:\(\d+\))?)\.([\w$]+)\s*\)\s*\(`)
	compactHelperRE  = regexp.MustCompile(`\bfunction\s+([\w$]+)\s*\(\s*([\w$]+)\s*,\s*([\w$]+)\s*,\s*([\w$]+)\s*,\s*([\w$]+)\s*,\s*([\w$]+)\s*\)\s*\{`)
	compactRuntimeRE = regexp.MustCompile(`^\s*[\w$.]+\.proto3\s*,`)
	compactTypeRE    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	compactRefRE     = regexp.MustCompile(`^[\w$]+(?:\.[\w$]+)?$`)
	compactRequireRE = regexp.MustCompile(`^([\w$]+)\((\d+)\)$`)
)

type compactClass struct {
	symbol, base, packageExpr, descriptor string
	refs                                  []string
	pos, module                           int
}

// extractCompactTypes 静态解释紧凑描述；先建立完整类型集合，再解析引用，不运行安装包代码。
func extractCompactTypes(text string, moduleInfos []moduleInfo, legacyMessages []Message, legacyEnums []Enum, wpg *webpackModuleGraph) ([]Message, []Enum, error) {
	starts := make([]int, len(moduleInfos))
	for i, info := range moduleInfos {
		starts[i] = info.Start
	}
	constants := make(map[int]map[string][]string)
	for _, m := range compactConstRE.FindAllStringSubmatchIndex(text, -1) {
		module := moduleStartForPos(starts, m[0])
		symbol := text[m[2]:m[3]]
		value, err := compactString(text[m[4]:m[5]])
		if err != nil {
			continue
		}
		if constants[module] == nil {
			constants[module] = make(map[string][]string)
		}
		constants[module][symbol] = append(constants[module][symbol], value)
	}
	classes := make(map[int]map[string][]*compactClass)
	var descriptors []*compactClass
	for _, m := range compactClassRE.FindAllStringSubmatchIndex(text, -1) {
		end, err := compactClose(text, m[1]-1)
		if err != nil {
			continue
		}
		body := text[m[1]:end]
		c := &compactClass{symbol: text[m[2]:m[3]], base: text[m[4]:m[5]], pos: m[0], module: moduleStartForPos(starts, m[0])}
		if p := compactPackageRE.FindStringSubmatch(body); p != nil {
			c.packageExpr = strings.TrimSpace(p[1])
		}
		if d := compactMethodRE.FindStringIndex(body); d != nil {
			arrayStart := m[1] + d[1]
			if arrayStart >= len(text) || text[arrayStart] != '[' {
				return nil, nil, fmt.Errorf("紧凑消息 %s 描述不是数组", c.symbol)
			}
			arrayEnd, err := compactClose(text, arrayStart)
			if err != nil {
				return nil, nil, err
			}
			tail := strings.TrimSpace(text[arrayEnd+1:])
			if strings.HasPrefix(tail, ";") {
				tail = strings.TrimSpace(tail[1:])
			}
			if !strings.HasPrefix(tail, "}") {
				return nil, nil, fmt.Errorf("紧凑消息 %s 描述含动态表达式", c.symbol)
			}
			items, err := compactSplit(text[arrayStart+1 : arrayEnd])
			if err != nil || len(items) == 0 {
				return nil, nil, fmt.Errorf("紧凑消息 %s 数组无效: %v", c.symbol, err)
			}
			c.descriptor, err = compactString(items[0])
			if err != nil {
				return nil, nil, fmt.Errorf("紧凑消息 %s 描述字符串: %w", c.symbol, err)
			}
			c.refs = items[1:]
			descriptors = append(descriptors, c)
		}
		if classes[c.module] == nil {
			classes[c.module] = make(map[string][]*compactClass)
		}
		classes[c.module][c.symbol] = append(classes[c.module][c.symbol], c)
	}
	var resolvePackage func(*compactClass, map[*compactClass]bool) (string, error)
	resolvePackage = func(c *compactClass, visiting map[*compactClass]bool) (string, error) {
		if visiting[c] {
			return "", fmt.Errorf("紧凑消息基类循环: %s", c.symbol)
		}
		visiting[c] = true
		defer delete(visiting, c)
		if c.packageExpr != "" {
			prefix, err := compactConstant(c.packageExpr, c.module, constants)
			if err != nil {
				return "", err
			}
			if !strings.HasSuffix(prefix, ".") || !compactTypeRE.MatchString(strings.TrimSuffix(prefix, ".")) {
				return "", fmt.Errorf("紧凑包名前缀无效: %q", prefix)
			}
			return prefix, nil
		}
		symbol, module, err := compactTarget(c.base, c.module, wpg)
		if err != nil {
			return "", err
		}
		bases := classes[module][symbol]
		if len(bases) != 1 {
			return "", fmt.Errorf("紧凑消息 %s 基类 %s 在模块 %d 中有 %d 个定义", c.symbol, c.base, module, len(bases))
		}
		return resolvePackage(bases[0], visiting)
	}
	var messages []Message
	for _, c := range descriptors {
		prefix, err := resolvePackage(c, make(map[*compactClass]bool))
		if err != nil {
			return nil, nil, err
		}
		name := strings.SplitN(c.descriptor, "|", 2)[0]
		if !compactTypeRE.MatchString(name) {
			return nil, nil, fmt.Errorf("紧凑消息名无效: %q", name)
		}
		messages = append(messages, Message{TypeName: prefix + name, VarName: c.symbol, InternalName: c.symbol, Package: strings.TrimSuffix(prefix, "."), ShortName: name, Pos: c.pos, ModuleStart: c.module})
	}
	enums, err := compactEnums(text, starts, constants, wpg)
	if err != nil {
		return nil, nil, err
	}
	allMessages := append(append([]Message{}, legacyMessages...), messages...)
	allEnums := append(append([]Enum{}, legacyEnums...), enums...)
	resolver := newTypeResolver(allMessages, allEnums, buildAliasIndex(text, moduleInfos))
	kinds := make(map[string]string)
	for _, m := range allMessages {
		kinds[m.TypeName] = "message"
	}
	for _, e := range allEnums {
		if kinds[e.TypeName] == "message" {
			return nil, nil, fmt.Errorf("类型同时是消息和枚举: %s", e.TypeName)
		}
		kinds[e.TypeName] = "enum"
	}
	for i, c := range descriptors {
		fields, err := compactFields(c, messages[i], resolver, kinds, wpg)
		if err != nil {
			return nil, nil, err
		}
		messages[i].Fields = fields
	}
	if err := validateDescriptorConflicts(append(append([]Message{}, legacyMessages...), messages...), allEnums, resolver, wpg); err != nil {
		return nil, nil, err
	}
	return messages, enums, nil
}

func compactConstant(expr string, module int, constants map[int]map[string][]string) (string, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, `"`) || strings.HasPrefix(expr, "'") {
		return compactString(expr)
	}
	values := uniqueSortedStrings(constants[module][expr])
	if len(values) != 1 {
		return "", fmt.Errorf("模块 %d 常量 %s 有 %d 个字符串定义", module, expr, len(values))
	}
	return values[0], nil
}

// compactTarget 只接受同模块局部符号或由 webpack 图唯一证明的命名空间引用。
func compactTarget(ref string, module int, g *webpackModuleGraph) (string, int, error) {
	ref = strings.TrimSpace(ref)
	if !compactRefRE.MatchString(ref) {
		return "", 0, fmt.Errorf("紧凑类型引用格式无效: %q", ref)
	}
	parts := strings.Split(ref, ".")
	if len(parts) == 1 {
		return ref, module, nil
	}
	if g == nil {
		return "", 0, fmt.Errorf("紧凑引用 %s 缺少 webpack 图", ref)
	}
	ids := uniqueSortedStrings(g.namespaceImports[g.moduleIDByStart[module]+":"+parts[0]])
	if len(ids) != 1 {
		return "", 0, fmt.Errorf("紧凑引用 %s 导入目标数量为 %d", ref, len(ids))
	}
	targets := g.resolveExportTargets(ids[0], parts[1], make(map[string]bool))
	if len(targets) != 1 {
		return "", 0, fmt.Errorf("紧凑引用 %s 导出目标数量为 %d", ref, len(targets))
	}
	start, ok := g.moduleStartForModuleID(targets[0].moduleID)
	if !ok {
		return "", 0, fmt.Errorf("紧凑引用 %s 目标模块不存在", ref)
	}
	return targets[0].symbol, start, nil
}

func compactFields(c *compactClass, msg Message, resolver *TypeResolver, kinds map[string]string, g *webpackModuleGraph) ([]Field, error) {
	resolve := func(token string) (string, any, error) {
		if !strings.HasPrefix(token, "#") {
			no, err := strconv.Atoi(token)
			if err != nil || scalarTypes[no] == "" {
				return "", nil, fmt.Errorf("未知标量 %q", token)
			}
			return "scalar", no, nil
		}
		index, err := strconv.Atoi(strings.TrimPrefix(token, "#"))
		if err != nil || index < 0 || index >= len(c.refs) {
			return "", nil, fmt.Errorf("引用下标无效: %s", token)
		}
		ref := strings.TrimSpace(c.refs[index])
		symbol, module, err := compactTarget(ref, c.module, g)
		if err != nil {
			return "", nil, err
		}
		defs := resolver.directDefinitions(symbol, module)
		if len(defs) == 0 {
			for _, def := range resolver.bySymbol[symbol] {
				if def.ModuleStart == module {
					defs = append(defs, def)
				}
			}
		}
		names := definitionTypeNames(defs)
		if len(names) != 1 {
			return "", nil, fmt.Errorf("引用 %s 在模块 %d 中解析到 %d 个类型", ref, module, len(names))
		}
		kind := kinds[names[0]]
		if kind == "" {
			return "", nil, fmt.Errorf("引用 %s 类型类别未知", ref)
		}
		return kind, names[0], nil
	}
	var result []Field
	seen := make(map[int]bool)
	names := make(map[string]bool)
	for _, raw := range strings.Split(c.descriptor, "|")[1:] {
		fail := func(err error) ([]Field, error) {
			return nil, fmt.Errorf("紧凑消息 %s 字段 %q: %w", msg.TypeName, raw, err)
		}
		parts := strings.Fields(raw)
		if len(parts) != 3 && len(parts) != 4 {
			return fail(fmt.Errorf("字段必须包含 3 或 4 个 token"))
		}
		no, err := strconv.Atoi(parts[0])
		if err != nil || no < 1 || no > 536870911 || (no >= 19000 && no <= 19999) || seen[no] {
			return fail(fmt.Errorf("字段号无效或重复"))
		}
		if !fieldNameRe.MatchString(parts[1]) || names[parts[1]] {
			return fail(fmt.Errorf("字段名无效或重复"))
		}
		seen[no] = true
		names[parts[1]] = true
		f := Field{No: no, Name: parts[1]}
		token := parts[2]
		modifier := ""
		if len(parts) == 4 {
			modifier = parts[3]
		}
		if strings.HasSuffix(token, "?") || strings.HasSuffix(token, "*") {
			if modifier != "" {
				return fail(fmt.Errorf("重复字段修饰符"))
			}
			modifier = token[len(token)-1:]
			token = token[:len(token)-1]
		}
		if strings.Contains(token, ",") {
			pair := strings.Split(token, ",")
			if len(pair) != 2 || modifier != "" {
				return fail(fmt.Errorf("map 格式或修饰符无效"))
			}
			key, err := strconv.Atoi(pair[0])
			if err != nil || scalarTypes[key] == "" || key == 1 || key == 2 || key == 12 {
				return fail(fmt.Errorf("map 键类型无效"))
			}
			kind, value, err := resolve(pair[1])
			if err != nil {
				return fail(err)
			}
			f.Kind = "map"
			f.MapKey = key
			f.MapValueKind = kind
			f.MapValueT = value
		} else {
			kind, value, err := resolve(token)
			if err != nil {
				return fail(err)
			}
			f.Kind = kind
			f.T = value
			switch modifier {
			case "?":
				f.Opt = true
			case "*":
				f.Repeated = true
			case "":
			default:
				if !oneofNameRe.MatchString(modifier) {
					return fail(fmt.Errorf("oneof 名无效"))
				}
				f.Oneof = modifier
			}
		}
		result = append(result, f)
	}
	return result, nil
}

func compactEnums(text string, starts []int, constants map[int]map[string][]string, g *webpackModuleGraph) ([]Enum, error) {
	helpers := compactEnumHelpers(text, starts)
	var enums []Enum
	for _, m := range compactEnumRE.FindAllStringSubmatchIndex(text, -1) {
		module := moduleStartForPos(starts, m[0])
		helper := text[m[4]:m[5]]
		export := text[m[6]:m[7]]
		symbol, targetModule, targetErr := compactHelperTarget(helper, export, module, g)
		known := targetErr == nil && helpers[fmt.Sprintf("%d:%s", targetModule, symbol)]
		// 其他普通函数调用不属于描述；以 proto3 开头的同形调用必须证明 helper 来源。
		if !known {
			lookEnd := m[1] + 150
			if lookEnd > len(text) {
				lookEnd = len(text)
			}
			if compactRuntimeRE.MatchString(text[m[1]:lookEnd]) {
				return nil, fmt.Errorf("紧凑枚举 helper %s.%s 来源或语义无法证明: %v", helper, export, targetErr)
			}
			continue
		}
		end, err := compactClose(text, m[1]-1)
		if err != nil {
			return nil, err
		}
		// 逗号包装仅允许前序 proto3 属性访问，helper 必须是赋值的最终值。
		beforeCall := text[m[0] : m[1]-1]
		wrappers := strings.Count(beforeCall, "(") - strings.Count(beforeCall, ")")
		remaining := strings.TrimSpace(text[end+1:])
		for i := 0; i < wrappers; i++ {
			if !strings.HasPrefix(remaining, ")") {
				return nil, fmt.Errorf("紧凑枚举 %s 的 helper 不是包装表达式最终值", text[m[2]:m[3]])
			}
			remaining = strings.TrimSpace(remaining[1:])
		}
		if remaining != "" && !strings.ContainsRune(",;}", rune(remaining[0])) {
			return nil, fmt.Errorf("紧凑枚举 %s 赋值含未知后续表达式", text[m[2]:m[3]])
		}
		args, err := compactSplit(text[m[1]:end])
		if err != nil || len(args) < 4 || len(args) > 5 {
			return nil, fmt.Errorf("紧凑枚举参数无效: %v", err)
		}
		prefix, err := compactConstant(args[1], module, constants)
		if err != nil {
			return nil, err
		}
		name, err := compactString(args[2])
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(prefix, ".") || !compactTypeRE.MatchString(prefix+name) {
			return nil, fmt.Errorf("紧凑枚举类型名无效: %s%s", prefix, name)
		}
		pairs, err := compactArray(args[3])
		if err != nil {
			return nil, err
		}
		e := Enum{TypeName: prefix + name, VarName: text[m[2]:m[3]], Package: strings.TrimSuffix(prefix, "."), ShortName: name, Pos: m[0], ModuleStart: module}
		valuePrefix := ""
		if len(args) == 5 {
			if args[4] == "1" {
				short := name[strings.LastIndex(name, ".")+1:]
				var b strings.Builder
				for i, c := range short {
					if i > 0 && c >= 'A' && c <= 'Z' {
						b.WriteByte('_')
					}
					b.WriteRune(c)
				}
				valuePrefix = strings.ToUpper(b.String()) + "_"
			} else {
				aliases, err := compactArray(args[4])
				if err != nil || len(aliases) != len(pairs) {
					return nil, fmt.Errorf("紧凑枚举 %s 别名格式或数量无效", e.TypeName)
				}
				for _, alias := range aliases {
					if _, err := compactString(alias); err != nil {
						return nil, fmt.Errorf("紧凑枚举 %s 别名不是静态字符串", e.TypeName)
					}
				}
			}
		}
		seenNames := make(map[string]bool)
		for _, pair := range pairs {
			items, err := compactArray(pair)
			if err != nil || len(items) != 2 {
				return nil, fmt.Errorf("紧凑枚举 %s 值格式无效", e.TypeName)
			}
			no, err := strconv.ParseInt(items[0], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("紧凑枚举 %s 数字无效: %w", e.TypeName, err)
			}
			value, err := compactString(items[1])
			if err != nil {
				return nil, err
			}
			value = valuePrefix + value
			if !fieldNameRe.MatchString(value) || seenNames[value] {
				return nil, fmt.Errorf("紧凑枚举 %s 值名无效或重复: %s", e.TypeName, value)
			}
			seenNames[value] = true
			e.Values = append(e.Values, EnumValue{No: int(no), Name: value})
		}
		if len(e.Values) == 0 {
			return nil, fmt.Errorf("紧凑枚举 %s 无值", e.TypeName)
		}
		enums = append(enums, e)
	}
	return enums, nil
}

// compactEnumHelpers 依据五个参数的实际语义识别 helper，不依赖压缩模块号或导出名。
func compactEnumHelpers(text string, starts []int) map[string]bool {
	helpers := make(map[string]bool)
	for _, m := range compactHelperRE.FindAllStringSubmatchIndex(text, -1) {
		end, err := compactClose(text, m[1]-1)
		if err != nil {
			continue
		}
		body := text[m[1]:end]
		if !strings.Contains(body, ".util.setEnumType") {
			continue
		}
		runtime := regexp.QuoteMeta(text[m[4]:m[5]])
		prefix := regexp.QuoteMeta(text[m[6]:m[7]])
		name := regexp.QuoteMeta(text[m[8]:m[9]])
		feature := regexp.MustCompile(`(?:^|[^\w$.])` + runtime + `\.util\.setEnumType\s*\(\s*[\w$]+\s*,\s*` + prefix + `\s*\+\s*` + name + `\s*,\s*[\w$]+\s*\)`)
		if feature.MatchString(body) {
			helpers[fmt.Sprintf("%d:%s", moduleStartForPos(starts, m[0]), text[m[2]:m[3]])] = true
		}
	}
	return helpers
}

func compactHelperTarget(helper, export string, module int, g *webpackModuleGraph) (string, int, error) {
	if direct := compactRequireRE.FindStringSubmatch(helper); direct != nil {
		if g == nil {
			return "", 0, fmt.Errorf("缺少 webpack 图")
		}
		if direct[1] != g.requireVarByStart[module] {
			return "", 0, fmt.Errorf("调用 %s 不是所属模块的 require", direct[1])
		}
		targets := g.resolveExportTargets(direct[2], export, make(map[string]bool))
		if len(targets) != 1 {
			return "", 0, fmt.Errorf("导出目标数量为 %d", len(targets))
		}
		start, ok := g.moduleStartForModuleID(targets[0].moduleID)
		if !ok {
			return "", 0, fmt.Errorf("目标模块不存在")
		}
		return targets[0].symbol, start, nil
	}
	return compactTarget(helper+"."+export, module, g)
}

func compactArray(text string) ([]string, error) {
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != '[' {
		return nil, fmt.Errorf("不是静态数组: %q", text)
	}
	end, err := compactClose(text, 0)
	if err != nil || end != len(text)-1 {
		return nil, fmt.Errorf("数组边界无效: %q", text)
	}
	return compactSplit(text[1:end])
}

func compactString(text string) (string, error) {
	text = strings.TrimSpace(text)
	if len(text) < 2 || (text[0] != '"' && text[0] != '\'') || text[len(text)-1] != text[0] {
		return "", fmt.Errorf("不是静态字符串: %q", text)
	}
	if text[0] == '\'' {
		text = `"` + strings.ReplaceAll(strings.ReplaceAll(text[1:len(text)-1], `\'`, `'`), `"`, `\"`) + `"`
	}
	return strconv.Unquote(text)
}

// compactClose 跳过字符串和注释匹配括号，避免描述字符串中的字符改变语法边界。
func compactClose(text string, start int) (int, error) {
	if start < 0 || start >= len(text) {
		return 0, fmt.Errorf("静态表达式起点越界")
	}
	var stack []byte
	quote := byte(0)
	for i := start; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '/' && i+1 < len(text) {
			if text[i+1] == '/' {
				for i < len(text) && text[i] != '\n' {
					i++
				}
				continue
			}
			if text[i+1] == '*' {
				end := strings.Index(text[i+2:], "*/")
				if end < 0 {
					break
				}
				i += end + 3
				continue
			}
		}
		switch c {
		case '(':
			stack = append(stack, ')')
		case '[':
			stack = append(stack, ']')
		case '{':
			stack = append(stack, '}')
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return 0, fmt.Errorf("静态表达式括号不匹配: %d", i)
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("静态表达式未闭合: %d", start)
}

func compactSplit(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var items []string
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"', '\'', '`':
			quote := text[i]
			closed := false
			for i++; i < len(text); i++ {
				if text[i] == '\\' {
					i++
					continue
				}
				if text[i] == quote {
					closed = true
					break
				}
			}
			if !closed {
				return nil, fmt.Errorf("静态字符串未闭合")
			}
		case '[', '{', '(':
			end, err := compactClose(text, i)
			if err != nil {
				return nil, err
			}
			i = end
		case ',':
			items = append(items, strings.TrimSpace(text[start:i]))
			start = i + 1
		}
	}
	items = append(items, strings.TrimSpace(text[start:]))
	for _, item := range items {
		if item == "" {
			return nil, fmt.Errorf("静态列表含空项")
		}
	}
	return items, nil
}
