package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
)

// isGooglePkg checks if a package is a Google standard package that should not be generated
func isGooglePkg(pkg string) bool {
	return pkg == "google.protobuf" || pkg == "google.rpc"
}

// Scalar type mapping
var scalarTypes = map[int]string{
	1:  "double",
	2:  "float",
	3:  "int64",
	4:  "uint64",
	5:  "int32",
	6:  "fixed64",
	7:  "fixed32",
	8:  "bool",
	9:  "string",
	12: "bytes",
	13: "uint32",
	15: "sfixed32",
	16: "sfixed64",
	17: "sint32",
	18: "sint64",
}

// ExtractionOptions controls extraction behavior.
type ExtractionOptions struct {
	// Strict enables validation that fails on unresolved/placeholder types,
	// skipped fields, and method type resolution errors. Default is true.
	Strict bool
}

// DefaultExtractionOptions returns options with strict mode enabled.
func DefaultExtractionOptions() ExtractionOptions {
	return ExtractionOptions{Strict: true}
}

type extractionDiagnostics struct {
	totalFieldObjects   int
	parsedFieldObjects  int
	skippedFieldObjects int
	skippedFieldSamples []string
	unresolvedTypeRefs  map[string]int
	emptyMessages       []string
	placeholderHits     []string
}

func newExtractionDiagnostics() *extractionDiagnostics {
	return &extractionDiagnostics{
		unresolvedTypeRefs: make(map[string]int),
	}
}

func (d *extractionDiagnostics) addSkippedField(fieldObject string, reason error) {
	if d == nil {
		return
	}
	d.totalFieldObjects++
	d.skippedFieldObjects++
	if len(d.skippedFieldSamples) < 20 {
		trimmed := strings.TrimSpace(fieldObject)
		if len(trimmed) > 140 {
			trimmed = trimmed[:140] + "..."
		}
		if reason != nil {
			d.skippedFieldSamples = append(d.skippedFieldSamples, fmt.Sprintf("%s | %s", reason.Error(), trimmed))
		} else {
			d.skippedFieldSamples = append(d.skippedFieldSamples, trimmed)
		}
	}
}

func (d *extractionDiagnostics) addParsedField() {
	if d == nil {
		return
	}
	d.totalFieldObjects++
	d.parsedFieldObjects++
}

func (d *extractionDiagnostics) addUnresolvedType(ref string) {
	if d == nil {
		return
	}
	key := strings.TrimSpace(ref)
	if key == "" {
		key = "<empty>"
	}
	d.unresolvedTypeRefs[key]++
}

type extractionRun struct {
	options     ExtractionOptions
	diagnostics *extractionDiagnostics
	copiedTypes map[string]map[string]string
}

func newExtractionRun(options ExtractionOptions) *extractionRun {
	return &extractionRun{
		options:     options,
		diagnostics: newExtractionDiagnostics(),
		copiedTypes: make(map[string]map[string]string),
	}
}

var (
	noRe          = regexp.MustCompile(`(?:^|[,{]\s*)no:\s*(\d+)`)
	nameRe        = regexp.MustCompile(`(?:^|[,{]\s*)name:\s*["']([^"']+)["']`)
	kindRe        = regexp.MustCompile(`(?:^|[,{]\s*)kind:\s*["']([^"']+)["']`)
	enumTypeRe    = regexp.MustCompile(`[,\s]T:\s*[\w$.]+\.getEnumType\s*\(\s*([\w$.]+)\s*\)`)
	tRe           = regexp.MustCompile(`[,\s]T:\s*([\w$.]+)`)
	oneofRe       = regexp.MustCompile(`oneof:\s*["']([^"']+)["']`)
	repeatedRe    = regexp.MustCompile(`repeated:\s*(!0|true)`)
	optRe         = regexp.MustCompile(`opt:\s*(!0|true)`)
	keyRe         = regexp.MustCompile(`[,\s]K:\s*(\d+)`)
	mapValueRe    = regexp.MustCompile(`V:\s*\{([^}]*)\}`)
	mapValueKRe   = regexp.MustCompile(`(?:^|[,{]\s*)kind:\s*["'](\w+)["']`)
	mapValueTRe   = regexp.MustCompile(`[,\s]T:\s*([\w$.]+)`)
	oneofNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	fieldNameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	placeholderRe = regexp.MustCompile(`^\s*(optional\s+|repeated\s+)?[A-Za-z_][A-Za-z0-9_.<>]*\s+(field_\d+|unknown(?:_[A-Za-z0-9_]+)?)\s*=\s*\d+\s*;`)
	varAliasRe    = regexp.MustCompile(`\b(?:let|const|var)\s+([\w$]+)\s*=\s*([\w$]+)\s*(?:[,;])`)
	streamCloseRe = regexp.MustCompile(`(?s)message\s+ExecClientControlMessage\s*\{.*?ExecClientStreamClose\s+stream_close\s*=\s*1\s*;`)
	shellStdoutRe = regexp.MustCompile(`(?s)message\s+ShellStream\s*\{.*?ShellStreamStdout\s+stdout\s*=\s*1\s*;`)
)

type Field struct {
	No           int    `json:"no"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	T            any    `json:"T"`     // int for scalar, string for message ref
	Oneof        string `json:"oneof"` // oneof group name
	Repeated     bool   `json:"repeated"`
	Opt          bool   `json:"opt"` // optional
	MapKey       int    `json:"K"`   // map key type (scalar type number)
	MapValueKind string // "scalar" or "message"
	MapValueT    any    // scalar type number or message var name
}

type Message struct {
	TypeName     string
	VarName      string // JS external variable name (e.g., tPe)
	InternalName string // JS internal class name (e.g., bd)
	Fields       []Field
	Package      string
	ShortName    string
	Pos          int
	ModuleStart  int
}

type Enum struct {
	TypeName    string
	VarName     string
	Values      []EnumValue
	Package     string
	ShortName   string
	Pos         int
	ModuleStart int
}

type EnumValue struct {
	No   int
	Name string
}

type Service struct {
	TypeName    string
	VarName     string
	Methods     []Method
	Package     string
	ShortName   string
	Pos         int
	ModuleStart int
}

type Method struct {
	Name       string
	InputType  string // variable name
	OutputType string // variable name
	Kind       string // Unary, ServerStreaming, ClientStreaming, BiDiStreaming
}

type symbolDef struct {
	TypeName    string
	Pos         int
	Kind        string
	ModuleStart int
	Alias       bool
}

type TypeResolver struct {
	bySymbol       map[string][]symbolDef
	byDirectSymbol map[string][]symbolDef
	byShort        map[string][]symbolDef
}

type aliasIndex struct {
	byModule map[int]map[string][]string
	legacy   map[string][]string
}

// ===== Webpack Module Graph =====

// moduleInfo records a webpack module's start position and numeric ID.
type moduleInfo struct {
	Start int
	ID    string
}

// webpackModuleGraph captures webpack module import/export relationships.
type webpackModuleGraph struct {
	namespaceImports map[string][]string // "contextModuleID:namespaceVar" -> target module IDs
	exportMaps       map[string][]string // "targetModuleID:exportName" -> internal symbols
	reexportModules  map[string][]string // module ID -> modules reached through __exportStar
	moduleIDByStart  map[int]string
	moduleStartByID  map[string]int
	moduleStarts     []int
}

type webpackExportTarget struct {
	moduleID string
	symbol   string
}

func (g *webpackModuleGraph) moduleStartForModuleID(id string) (int, bool) {
	start, found := g.moduleStartByID[id]
	return start, found
}

func (g *webpackModuleGraph) moduleStartsFromInfos() []int {
	return g.moduleStarts
}

func (g *webpackModuleGraph) resolveExportTargets(moduleID string, exportName string, visiting map[string]bool) []webpackExportTarget {
	visitKey := moduleID + ":" + exportName
	if visiting[visitKey] {
		return nil
	}
	visiting[visitKey] = true
	defer delete(visiting, visitKey)

	directSymbols := uniqueSortedStrings(g.exportMaps[moduleID+":"+exportName])
	if len(directSymbols) > 0 {
		targets := make([]webpackExportTarget, 0, len(directSymbols))
		for _, symbol := range directSymbols {
			targets = append(targets, webpackExportTarget{moduleID: moduleID, symbol: symbol})
		}
		return targets
	}

	var targets []webpackExportTarget
	for _, reexportModuleID := range uniqueSortedStrings(g.reexportModules[moduleID]) {
		targets = append(targets, g.resolveExportTargets(reexportModuleID, exportName, visiting)...)
	}
	return uniqueSortedExportTargets(targets)
}

func uniqueSortedExportTargets(targets []webpackExportTarget) []webpackExportTarget {
	byKey := make(map[string]webpackExportTarget, len(targets))
	keys := make([]string, 0, len(targets))
	for _, target := range targets {
		key := target.moduleID + ":" + target.symbol
		if _, exists := byKey[key]; exists {
			continue
		}
		byKey[key] = target
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]webpackExportTarget, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

// resolveQualifiedRef resolves a qualified ref (e.g., r.KS) through the webpack module graph.
func (g *webpackModuleGraph) resolveQualifiedRef(
	namespaceVar, exportName string,
	contextModuleStart int,
	resolver *TypeResolver,
	preferredPkg string,
	expectedKind string,
) (string, error) {
	if g == nil {
		return "", errors.New("webpack module graph is unavailable")
	}

	contextModuleID := g.moduleIDByStart[contextModuleStart]
	if contextModuleID == "" {
		return "", fmt.Errorf("webpack module for source position %d was not found", contextModuleStart)
	}

	nsKey := contextModuleID + ":" + namespaceVar
	targetModuleIDs := uniqueSortedStrings(g.namespaceImports[nsKey])
	if len(targetModuleIDs) == 0 {
		return "", fmt.Errorf("webpack module %s has no namespace import %q", contextModuleID, namespaceVar)
	}
	if len(targetModuleIDs) > 1 {
		return "", fmt.Errorf(
			"webpack module %s namespace import %q has multiple targets: %s",
			contextModuleID,
			namespaceVar,
			strings.Join(targetModuleIDs, ", "),
		)
	}
	targetModuleID := targetModuleIDs[0]

	exportTargets := g.resolveExportTargets(targetModuleID, exportName, make(map[string]bool))
	if len(exportTargets) == 0 {
		return "", fmt.Errorf("webpack module %s has no export %q", targetModuleID, exportName)
	}
	if len(exportTargets) > 1 {
		targetNames := make([]string, 0, len(exportTargets))
		for _, target := range exportTargets {
			if target.moduleID == targetModuleID {
				targetNames = append(targetNames, target.symbol)
			} else {
				targetNames = append(targetNames, target.moduleID+":"+target.symbol)
			}
		}
		return "", fmt.Errorf(
			"webpack module %s export %q has multiple targets: %s",
			targetModuleID,
			exportName,
			strings.Join(targetNames, ", "),
		)
	}
	exportTarget := exportTargets[0]
	internalSymbol := exportTarget.symbol

	targetModuleStart, found := g.moduleStartForModuleID(exportTarget.moduleID)
	if !found {
		return "", fmt.Errorf("webpack export %q targets unknown module %s", exportName, exportTarget.moduleID)
	}

	candidates := resolver.directDefinitions(internalSymbol, targetModuleStart)
	if len(candidates) == 0 {
		return "", fmt.Errorf(
			"webpack module %s export %q targets %q with no internal definition",
			exportTarget.moduleID,
			exportName,
			internalSymbol,
		)
	}

	if expectedKind != "" {
		kindCandidates := filterDefinitionsByKind(candidates, expectedKind)
		if len(kindCandidates) == 0 {
			kinds := definitionKinds(candidates)
			return "", fmt.Errorf(
				"export %q from webpack module %s resolves to %s, expected %s",
				exportName,
				exportTarget.moduleID,
				strings.Join(kinds, " or "),
				expectedKind,
			)
		}
		candidates = kindCandidates
	}

	if preferredPkg != "" {
		packageCandidates := filterDefinitionsByPackage(candidates, preferredPkg)
		if len(packageCandidates) > 0 {
			candidates = packageCandidates
		}
	}

	typeNames := definitionTypeNames(candidates)
	if len(typeNames) != 1 {
		return "", fmt.Errorf(
			"webpack module %s export %q target %q resolves ambiguously to: %s",
			exportTarget.moduleID,
			exportName,
			internalSymbol,
			strings.Join(typeNames, ", "),
		)
	}
	return typeNames[0], nil
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func filterDefinitionsByKind(candidates []symbolDef, kind string) []symbolDef {
	result := make([]symbolDef, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Kind == kind {
			result = append(result, candidate)
		}
	}
	return result
}

func filterDefinitionsByPackage(candidates []symbolDef, pkg string) []symbolDef {
	result := make([]symbolDef, 0, len(candidates))
	for _, candidate := range candidates {
		candidatePkg, _ := parseTypeName(candidate.TypeName)
		if candidatePkg == pkg {
			result = append(result, candidate)
		}
	}
	return result
}

func definitionKinds(candidates []symbolDef) []string {
	kinds := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		kinds = append(kinds, candidate.Kind)
	}
	return uniqueSortedStrings(kinds)
}

func definitionTypeNames(candidates []symbolDef) []string {
	typeNames := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		typeNames = append(typeNames, candidate.TypeName)
	}
	return uniqueSortedStrings(typeNames)
}

func newTypeResolver(messages []Message, enums []Enum, aliases *aliasIndex) *TypeResolver {
	resolver := &TypeResolver{
		bySymbol:       make(map[string][]symbolDef),
		byDirectSymbol: make(map[string][]symbolDef),
		byShort:        make(map[string][]symbolDef),
	}

	add := func(symbol, typeName string, pos int, moduleStart int, kind string, direct bool) {
		symbol = strings.TrimSpace(symbol)
		typeName = strings.TrimSpace(typeName)
		if symbol == "" || typeName == "" {
			return
		}
		def := symbolDef{TypeName: typeName, Pos: pos, ModuleStart: moduleStart, Kind: kind, Alias: !direct}
		resolver.bySymbol[symbol] = append(resolver.bySymbol[symbol], def)
		if direct {
			resolver.byDirectSymbol[symbol] = append(resolver.byDirectSymbol[symbol], def)
		}
		_, shortName := parseTypeName(typeName)
		if shortName != "" {
			resolver.byShort[shortName] = append(resolver.byShort[shortName], def)
			underscoreAlias := strings.ReplaceAll(shortName, ".", "_")
			if underscoreAlias != shortName {
				resolver.byShort[underscoreAlias] = append(resolver.byShort[underscoreAlias], def)
			}
			if idx := strings.LastIndex(shortName, "."); idx > 0 && idx+1 < len(shortName) {
				resolver.byShort[shortName[idx+1:]] = append(resolver.byShort[shortName[idx+1:]], def)
			}
			if idx := strings.LastIndex(underscoreAlias, "_"); idx > 0 && idx+1 < len(underscoreAlias) {
				resolver.byShort[underscoreAlias[idx+1:]] = append(resolver.byShort[underscoreAlias[idx+1:]], def)
			}
		}
	}

	for _, msg := range messages {
		add(msg.VarName, msg.TypeName, msg.Pos, msg.ModuleStart, "message", true)
		if msg.InternalName != "" && msg.InternalName != msg.VarName {
			add(msg.InternalName, msg.TypeName, msg.Pos, msg.ModuleStart, "message", true)
		}
		for _, alias := range aliasesForSymbols(aliases, msg.ModuleStart, msg.VarName, msg.InternalName) {
			add(alias, msg.TypeName, msg.Pos, msg.ModuleStart, "message", false)
		}
	}
	for _, enum := range enums {
		add(enum.VarName, enum.TypeName, enum.Pos, enum.ModuleStart, "enum", true)
		for _, alias := range aliasesForSymbols(aliases, enum.ModuleStart, enum.VarName) {
			add(alias, enum.TypeName, enum.Pos, enum.ModuleStart, "enum", false)
		}
	}

	return resolver
}

func (resolver *TypeResolver) directDefinitions(symbol string, moduleStart int) []symbolDef {
	if resolver == nil {
		return nil
	}
	candidates := resolver.byDirectSymbol[strings.TrimSpace(symbol)]
	result := make([]symbolDef, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ModuleStart == moduleStart {
			result = append(result, candidate)
		}
	}
	return result
}

func buildAliasBucket(matches [][]string) map[string][]string {
	if len(matches) == 0 {
		return nil
	}

	direct := make(map[string]string, len(matches))
	for _, match := range matches {
		alias := strings.TrimSpace(match[1])
		target := strings.TrimSpace(match[2])
		if alias == "" || target == "" || alias == target {
			continue
		}
		direct[alias] = target
	}

	resolveRoot := func(symbol string) string {
		seen := make(map[string]bool)
		current := symbol
		for {
			if seen[current] {
				return symbol
			}
			seen[current] = true
			next := direct[current]
			if next == "" {
				return current
			}
			current = next
		}
	}

	aliases := make(map[string][]string)
	for alias := range direct {
		root := resolveRoot(alias)
		if root == alias {
			continue
		}
		aliases[root] = append(aliases[root], alias)
	}
	for root := range aliases {
		sort.Strings(aliases[root])
	}
	return aliases
}

func buildAliasIndex(text string, moduleInfos []moduleInfo) *aliasIndex {
	matches := varAliasRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	toSubmatches := func(items [][]int) [][]string {
		result := make([][]string, 0, len(items))
		for _, match := range items {
			result = append(result, []string{
				text[match[0]:match[1]],
				text[match[2]:match[3]],
				text[match[4]:match[5]],
			})
		}
		return result
	}

	if len(moduleInfos) == 0 {
		return &aliasIndex{legacy: buildAliasBucket(toSubmatches(matches))}
	}

	moduleStarts := make([]int, len(moduleInfos))
	validStarts := make(map[int]bool, len(moduleInfos))
	for index, info := range moduleInfos {
		moduleStarts[index] = info.Start
		validStarts[info.Start] = true
	}
	matchesByModule := make(map[int][][]int)
	for _, match := range matches {
		moduleStart := moduleStartForPos(moduleStarts, match[0])
		if !validStarts[moduleStart] {
			continue
		}
		matchesByModule[moduleStart] = append(matchesByModule[moduleStart], match)
	}

	index := &aliasIndex{byModule: make(map[int]map[string][]string)}
	for moduleStart, moduleMatches := range matchesByModule {
		index.byModule[moduleStart] = buildAliasBucket(toSubmatches(moduleMatches))
	}
	return index
}

func aliasesForSymbols(aliases *aliasIndex, moduleStart int, symbols ...string) []string {
	if aliases == nil {
		return nil
	}
	bucket := aliases.legacy
	if aliases.byModule != nil {
		bucket = aliases.byModule[moduleStart]
	}
	if len(bucket) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var result []string
	for _, symbol := range symbols {
		for _, alias := range bucket[strings.TrimSpace(symbol)] {
			if alias == "" || seen[alias] {
				continue
			}
			seen[alias] = true
			result = append(result, alias)
		}
	}
	sort.Strings(result)
	return result
}

func looksLikeFullTypeName(ref string) bool {
	trimmed := strings.TrimSpace(ref)
	if strings.HasPrefix(trimmed, "google.protobuf.") || strings.HasPrefix(trimmed, "google.rpc.") {
		return true
	}
	matched, _ := regexp.MatchString(`^[\w.]+\.v\d+\.[\w.]+$`, trimmed)
	return matched
}

func pickBestDefinition(candidates []symbolDef, contextPos int, contextModuleStart int, preferredPkg string, expectedKind string) (symbolDef, bool) {
	if len(candidates) == 0 {
		return symbolDef{}, false
	}

	filtered := candidates
	if strings.TrimSpace(preferredPkg) != "" {
		tmp := make([]symbolDef, 0, len(candidates))
		for _, item := range candidates {
			pkg, _ := parseTypeName(item.TypeName)
			if pkg == preferredPkg {
				tmp = append(tmp, item)
			}
		}
		if len(tmp) > 0 {
			filtered = tmp
		}
	}

	if strings.TrimSpace(expectedKind) != "" {
		tmp := make([]symbolDef, 0, len(filtered))
		for _, item := range filtered {
			if item.Kind == expectedKind {
				tmp = append(tmp, item)
			}
		}
		if len(tmp) == 0 {
			// Strict: when expectedKind is specified, never fall back to a different kind.
			return symbolDef{}, false
		}
		filtered = tmp
	}

	tmp := make([]symbolDef, 0, len(filtered))
	for _, item := range filtered {
		if item.ModuleStart == contextModuleStart {
			tmp = append(tmp, item)
		}
	}
	if len(tmp) > 0 {
		filtered = tmp
	}

	// Pick absolute nearest definition, prefer previous if distance ties.
	bestIndex := -1
	bestDistance := 0
	bestIsFuture := false
	for index, item := range filtered {
		distance := absInt(item.Pos - contextPos)
		isFuture := item.Pos > contextPos
		if bestIndex == -1 {
			bestIndex = index
			bestDistance = distance
			bestIsFuture = isFuture
			continue
		}
		if distance < bestDistance {
			bestIndex = index
			bestDistance = distance
			bestIsFuture = isFuture
			continue
		}
		if distance == bestDistance {
			// Same distance: prefer previous definition over future.
			if bestIsFuture && !isFuture {
				bestIndex = index
				bestIsFuture = isFuture
			}
		}
	}
	if bestIndex < 0 {
		return symbolDef{}, false
	}
	return filtered[bestIndex], true
}

func (resolver *TypeResolver) ResolveTypeName(ref string, contextPos int, contextModuleStart int, preferredPkg string, expectedKind string) (string, bool) {
	if resolver == nil {
		return "", false
	}

	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", false
	}
	if looksLikeFullTypeName(trimmed) {
		return trimmed, true
	}

	resolveBySymbol := func(symbol string, preferSameModule bool) (string, bool) {
		candidates := resolver.bySymbol[symbol]
		if len(candidates) == 0 {
			return "", false
		}
		if preferSameModule {
			filtered := make([]symbolDef, 0, len(candidates))
			for _, candidate := range candidates {
				if !candidate.Alias || candidate.ModuleStart == contextModuleStart {
					filtered = append(filtered, candidate)
				}
			}
			candidates = filtered
			if len(candidates) == 0 {
				return "", false
			}
		}
		moduleStart := 0
		if preferSameModule {
			moduleStart = contextModuleStart
		}
		best, ok := pickBestDefinition(candidates, contextPos, moduleStart, preferredPkg, expectedKind)
		if !ok {
			return "", false
		}
		return best.TypeName, true
	}
	resolveByShort := func(symbol string, preferSameModule bool) (string, bool) {
		candidates := resolver.byShort[symbol]
		if len(candidates) == 0 {
			return "", false
		}
		moduleStart := 0
		if preferSameModule {
			moduleStart = contextModuleStart
		}
		best, ok := pickBestDefinition(candidates, contextPos, moduleStart, preferredPkg, expectedKind)
		if !ok {
			return "", false
		}
		return best.TypeName, true
	}

	if typeName, ok := resolveBySymbol(trimmed, !strings.Contains(trimmed, ".")); ok {
		return typeName, true
	}
	if typeName, ok := resolveByShort(trimmed, !strings.Contains(trimmed, ".")); ok {
		return typeName, true
	}

	if strings.Contains(trimmed, ".") {
		parts := strings.Split(trimmed, ".")
		last := parts[len(parts)-1]
		if typeName, ok := resolveByShort(last, false); ok {
			return typeName, true
		}
		first := parts[0]
		if typeName, ok := resolveBySymbol(first, false); ok {
			return typeName, true
		}
	}

	return "", false
}

// isQualifiedRef checks if a type reference contains a dot (qualified ref like r.KS).
func isQualifiedRef(ref string) bool {
	return strings.Contains(strings.TrimSpace(ref), ".")
}

// resolveTypeNameWithGraph resolves a type reference, dispatching qualified refs
// through the webpack module graph and unqualified refs through the standard resolver.
func resolveTypeNameWithGraph(ref string, contextPos int, contextModuleStart int, resolver *TypeResolver, preferredPkg string, expectedKind string, wpg *webpackModuleGraph) (string, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", errors.New("empty type reference")
	}
	if looksLikeFullTypeName(trimmed) {
		return trimmed, nil
	}

	// Qualified ref: dispatch to webpack graph.
	if isQualifiedRef(trimmed) {
		parts := strings.SplitN(trimmed, ".", 2)
		return wpg.resolveQualifiedRef(
			parts[0],
			parts[1],
			contextModuleStart,
			resolver,
			preferredPkg,
			expectedKind,
		)
	}

	// Unqualified ref: use standard resolver.
	typeName, ok := resolver.ResolveTypeName(trimmed, contextPos, contextModuleStart, preferredPkg, expectedKind)
	if !ok {
		return "", fmt.Errorf("cannot resolve %q to %s descriptor", trimmed, expectedKind)
	}
	return typeName, nil
}

func fallbackTypeToken(ref string) string {
	token := strings.TrimSpace(ref)
	if token == "" {
		return token
	}
	if strings.Contains(token, ".") {
		parts := strings.Split(token, ".")
		return parts[len(parts)-1]
	}
	return token
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

var moduleStartRe = regexp.MustCompile(`(?:[{},;]|^|
)\s*(\d+):(?:function\([\w$,]*\)\s*\{|\([\w$,]*\)\s*=>\s*\{|[\w$]+\s*=>\s*\{)`)

func buildModuleStarts(text string) []int {
	matches := moduleStartRe.FindAllStringSubmatchIndex(text, -1)
	starts := make([]int, 0, len(matches))
	for _, match := range matches {
		starts = append(starts, match[0])
	}
	return starts
}

func moduleStartForPos(moduleStarts []int, pos int) int {
	if len(moduleStarts) == 0 {
		return 0
	}
	index := sort.Search(len(moduleStarts), func(i int) bool {
		return moduleStarts[i] > pos
	}) - 1
	if index < 0 {
		return 0
	}
	return moduleStarts[index]
}

// buildModuleInfos captures both module start positions and module IDs.
func buildModuleInfos(text string) []moduleInfo {
	matches := moduleStartRe.FindAllStringSubmatchIndex(text, -1)
	infos := make([]moduleInfo, 0, len(matches))
	for _, match := range matches {
		moduleID := text[match[2]:match[3]]
		infos = append(infos, moduleInfo{Start: match[0], ID: moduleID})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Start < infos[j].Start })
	return infos
}

var webpackNsImportRe = regexp.MustCompile(`(?:\b(?:var|let|const)\s+|,\s*)([\w$]+)\s*=\s*n\((\d+)\)`)
var webpackExportEntryRe = regexp.MustCompile(`([\w$]+)\s*:\s*\(\)\s*=>\s*([\w$]+)`)
var webpackExportMapRe = regexp.MustCompile(`n\.d\s*\(\s*t\s*,\s*\{([^}]+)\}\s*\)`)
var webpackCommonJSExportRe = regexp.MustCompile(`(?:\bt|exports)\.([\w$]+)\s*=\s*([\w$]+)\s*[,;]`)
var webpackExportStarHelperRe = regexp.MustCompile(`(?:\bvar\s+|,\s*)([\w$]+)\s*=\s*this&&this\.__exportStar\|\|function`)
var webpackExportStarCallRe = regexp.MustCompile(`([\w$]+)\s*\(\s*n\((\d+)\)\s*,\s*t\s*\)`)

// buildWebpackGraph extracts namespace imports and export maps from webpack bundles.
func buildWebpackGraph(text string, moduleInfos []moduleInfo) *webpackModuleGraph {
	g := &webpackModuleGraph{
		namespaceImports: make(map[string][]string),
		exportMaps:       make(map[string][]string),
		reexportModules:  make(map[string][]string),
		moduleIDByStart:  make(map[int]string),
		moduleStartByID:  make(map[string]int),
		moduleStarts:     make([]int, len(moduleInfos)),
	}

	for i, mi := range moduleInfos {
		g.moduleIDByStart[mi.Start] = mi.ID
		g.moduleStartByID[mi.ID] = mi.Start
		g.moduleStarts[i] = mi.Start
	}

	for i := 0; i < len(moduleInfos); i++ {
		mi := moduleInfos[i]
		bodyEnd := len(text)
		if i+1 < len(moduleInfos) {
			bodyEnd = moduleInfos[i+1].Start
		}

		// Skip past the module header to find the function body opening brace.
		// Start from mi.Start + 1 to skip the prefix "{" when the match starts with one
		// (e.g., fixture: {5801:(e,t,n)=>{...}}).
		bodyStart := mi.Start
		for j := mi.Start + 1; j < bodyEnd && j < mi.Start+200; j++ {
			if text[j] == '{' {
				bodyStart = j + 1
				break
			}
		}
		moduleBody := text[bodyStart:bodyEnd]

		// Extract namespace imports within this module
		nsMatches := webpackNsImportRe.FindAllStringSubmatch(moduleBody, -1)
		for _, ns := range nsMatches {
			nsVar := strings.TrimSpace(ns[1])
			nsModuleID := strings.TrimSpace(ns[2])
			key := mi.ID + ":" + nsVar
			g.namespaceImports[key] = append(g.namespaceImports[key], nsModuleID)
		}

		// Extract export maps: n.d(t, { ... })
		expMatches := webpackExportMapRe.FindAllStringSubmatch(moduleBody, -1)
		for _, em := range expMatches {
			exportsBody := em[1]
			entryMatches := webpackExportEntryRe.FindAllStringSubmatch(exportsBody, -1)
			for _, entry := range entryMatches {
				exportName := strings.TrimSpace(entry[1])
				internalSymbol := strings.TrimSpace(entry[2])
				key := mi.ID + ":" + exportName
				g.exportMaps[key] = append(g.exportMaps[key], internalSymbol)
			}
		}

		// CommonJS output modules assign exports directly (for example t.Empty=o).
		commonJSMatches := webpackCommonJSExportRe.FindAllStringSubmatch(moduleBody, -1)
		for _, match := range commonJSMatches {
			exportName := strings.TrimSpace(match[1])
			internalSymbol := strings.TrimSpace(match[2])
			if internalSymbol == "" || internalSymbol == "void" || internalSymbol == "undefined" {
				continue
			}
			key := mi.ID + ":" + exportName
			g.exportMaps[key] = append(g.exportMaps[key], internalSymbol)
		}

		// CommonJS barrel modules re-export complete downstream modules through
		// the webpack-transpiled __exportStar helper.
		exportStarHelpers := make(map[string]bool)
		for _, match := range webpackExportStarHelperRe.FindAllStringSubmatch(moduleBody, -1) {
			exportStarHelpers[strings.TrimSpace(match[1])] = true
		}
		for _, match := range webpackExportStarCallRe.FindAllStringSubmatch(moduleBody, -1) {
			if !exportStarHelpers[strings.TrimSpace(match[1])] {
				continue
			}
			g.reexportModules[mi.ID] = append(g.reexportModules[mi.ID], strings.TrimSpace(match[2]))
		}
	}

	return g
}

// ExtractProtosToDir extracts proto definitions from formatted JS file
// and writes generated .proto files to outputDir. Returns an error instead
// of exiting on fatal conditions so callers can handle failures gracefully.
func ExtractProtosToDir(inputFile, outputDir string) error {
	return ExtractProtosToDirWithOptions(inputFile, outputDir, DefaultExtractionOptions())
}

// ExtractProtosToDirWithOptions is the options-bearing variant of ExtractProtosToDir.
func ExtractProtosToDirWithOptions(inputFile, outputDir string, opts ExtractionOptions) error {
	return newExtractionRun(opts).extractProtosToDir(inputFile, outputDir)
}

func (run *extractionRun) extractProtosToDir(inputFile, outputDir string) error {
	content, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("read input file: %w", err)
	}

	text := string(content)
	moduleInfos := buildModuleInfos(text)
	wpg := buildWebpackGraph(text, moduleInfos)
	moduleStarts := wpg.moduleStartsFromInfos()
	aliases := buildAliasIndex(text, moduleInfos)

	// Extract messages, enums, and services
	messages := extractMessages(text, moduleStarts, run.diagnostics)
	enums := extractEnums(text, moduleStarts)
	services := extractServices(text, moduleStarts)
	for _, msg := range messages {
		if len(msg.Fields) == 0 {
			run.diagnostics.emptyMessages = append(run.diagnostics.emptyMessages, msg.TypeName)
		}
	}

	resolver := newTypeResolver(messages, enums, aliases)

	// Validate method types before generating protos
	methodErrs := validateMethodTypes(services, resolver, wpg)
	if len(methodErrs) > 0 {
		for _, me := range methodErrs {
			fmt.Fprintf(os.Stderr, "Method type error: %v\n", me)
		}
		if run.options.Strict {
			return fmt.Errorf("method type validation failed: %d error(s): %s", len(methodErrs), errors.Join(methodErrs...).Error())
		}
	}

	// Generate proto files
	if err := generateProtos(messages, enums, services, resolver, wpg, run, outputDir); err != nil {
		return fmt.Errorf("generate protos: %w", err)
	}

	validateErr := validateGeneratedProtos(outputDir, run.diagnostics)

	printDiagnosticsSummary(run.diagnostics)

	if run.options.Strict && hasValidationFailure(run.diagnostics, validateErr) {
		if validateErr != nil {
			return fmt.Errorf("validation failed: %w", validateErr)
		}
		return fmt.Errorf("validation failed: unresolved/placeholder output detected")
	}

	if validateErr != nil {
		fmt.Fprintf(os.Stderr, "Validation warning: %v\n", validateErr)
	}

	fmt.Printf("提取完成: %d 个消息, %d 个枚举, %d 个服务\n", len(messages), len(enums), len(services))
	return nil
}

// validateMethodType checks a single method input or output type reference resolves
// strictly to a message descriptor. It is the shared helper used for both directions.
func validateMethodType(svcName, methodName, dir, ref string, svcPos, svcModuleStart int, preferredPkg string, resolver *TypeResolver, wpg *webpackModuleGraph) error {
	typeName, err := resolveTypeNameWithGraph(ref, svcPos, svcModuleStart, resolver, preferredPkg, "message", wpg)
	if err != nil {
		return fmt.Errorf("service %s method %s %s type %q: %w", svcName, methodName, dir, ref, err)
	}
	// Defense in depth: verify the resolved type is not an enum.
	// (Strict kind in pickBestDefinition already prevents this, but check bySymbol
	// in case a future code path bypasses the kind filter.)
	defs, exists := resolver.bySymbol[ref]
	if exists {
		for _, def := range defs {
			if def.TypeName == typeName && def.Kind == "enum" {
				return fmt.Errorf("service %s method %s %s type %q: resolved to enum %q, expected message", svcName, methodName, dir, ref, typeName)
			}
		}
	}
	return nil
}

// validateMethodTypes checks that all service method input/output types resolve
// strictly to message descriptors. Enums and ambiguous resolutions are errors.
func validateMethodTypes(services []Service, resolver *TypeResolver, wpg *webpackModuleGraph) []error {
	var errs []error
	for _, svc := range services {
		for _, m := range svc.Methods {
			if err := validateMethodType(svc.TypeName, m.Name, "input", m.InputType, svc.Pos, svc.ModuleStart, svc.Package, resolver, wpg); err != nil {
				errs = append(errs, err)
			}
			if err := validateMethodType(svc.TypeName, m.Name, "output", m.OutputType, svc.Pos, svc.ModuleStart, svc.Package, resolver, wpg); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func hasValidationFailure(diag *extractionDiagnostics, validateErr error) bool {
	if validateErr != nil {
		return true
	}
	if diag == nil {
		return false
	}
	if diag.skippedFieldObjects > 0 {
		return true
	}
	if len(diag.unresolvedTypeRefs) > 0 {
		return true
	}
	if len(diag.placeholderHits) > 0 {
		return true
	}
	return false
}

func printDiagnosticsSummary(diag *extractionDiagnostics) {
	if diag == nil {
		return
	}

	fmt.Printf(
		"诊断汇总: fields %d/%d 解析成功, skipped=%d, unresolved=%d, placeholders=%d, empty_messages=%d\n",
		diag.parsedFieldObjects,
		diag.totalFieldObjects,
		diag.skippedFieldObjects,
		len(diag.unresolvedTypeRefs),
		len(diag.placeholderHits),
		len(diag.emptyMessages),
	)

	if diag.skippedFieldObjects > 0 && len(diag.skippedFieldSamples) > 0 {
		fmt.Println("字段解析失败样例:")
		for _, sample := range diag.skippedFieldSamples {
			fmt.Printf("  - %s\n", sample)
		}
	}

	if len(diag.unresolvedTypeRefs) > 0 {
		keys := make([]string, 0, len(diag.unresolvedTypeRefs))
		for key := range diag.unresolvedTypeRefs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Println("未解析类型引用:")
		for _, key := range keys {
			fmt.Printf("  - %s (%d)\n", key, diag.unresolvedTypeRefs[key])
		}
	}

	if len(diag.placeholderHits) > 0 {
		fmt.Println("占位字段命中:")
		for i, hit := range diag.placeholderHits {
			if i >= 20 {
				fmt.Printf("  - ... and %d more\n", len(diag.placeholderHits)-20)
				break
			}
			fmt.Printf("  - %s\n", hit)
		}
	}
}

func validateGeneratedProtos(outputDir string, diag *extractionDiagnostics) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("read output dir failed: %w", err)
	}

	protoFiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".proto") {
			protoFiles = append(protoFiles, name)
		}
	}
	if len(protoFiles) == 0 {
		return errors.New("no generated proto files found")
	}
	sort.Strings(protoFiles)

	for _, file := range protoFiles {
		body, readErr := os.ReadFile(filepath.Join(outputDir, file))
		if readErr != nil {
			return fmt.Errorf("read generated proto failed: %s: %w", file, readErr)
		}
		lines := strings.Split(string(body), "\n")
		for idx, line := range lines {
			if placeholderRe.MatchString(line) && diag != nil {
				hit := fmt.Sprintf("%s:%d: %s", file, idx+1, strings.TrimSpace(line))
				diag.placeholderHits = append(diag.placeholderHits, hit)
			}
		}
		if err := validateRequiredAgentShapes(file, string(body)); err != nil {
			return err
		}
	}

	parser := protoparse.Parser{
		ImportPaths:  []string{outputDir},
		LookupImport: desc.LoadFileDescriptor,
	}
	if _, parseErr := parser.ParseFiles(protoFiles...); parseErr != nil {
		return fmt.Errorf("parse generated proto failed: %w", parseErr)
	}

	return nil
}

func validateRequiredAgentShapes(file string, body string) error {
	if strings.Contains(body, "message ExecClientControlMessage") && !streamCloseRe.MatchString(body) {
		return fmt.Errorf("%s: ExecClientControlMessage.stream_close must be ExecClientStreamClose", file)
	}
	if strings.Contains(body, "message ShellStream") && !shellStdoutRe.MatchString(body) {
		return fmt.Errorf("%s: ShellStream.stdout must be ShellStreamStdout", file)
	}
	return nil
}

func extractMessages(text string, moduleStarts []int, diagnostics *extractionDiagnostics) []Message {
	var messages []Message

	// Pattern 1: VarName = class InternalName extends l { ... this.typeName = "..." ... this.fields = ... }
	// 先找所有 "变量名 = class 内部类名" 定义
	// JS 变量名可以包含 $ 符号，如 B$e, qg 等
	// 需要同时捕获外部变量名和内部类名，因为字段引用可能用任一个
	classDefRe := regexp.MustCompile(`([\w$]+)\s*=\s*class\s+([\w$]+)\s+extends\s+[\w$.]+\s*\{`)
	classMatches := classDefRe.FindAllStringSubmatchIndex(text, -1)

	// Pattern: this.typeName = "xxx.v1.YYY" (any package)
	typeNameRe := regexp.MustCompile(`this\.typeName\s*=\s*"([\w.]+)"`)

	// Pattern: this.fields = n.util.newFieldList(() => [...])
	fieldsRe := regexp.MustCompile(`this\.fields\s*=\s*\w+(?:\.proto3)?\.util\.newFieldList\s*\(\s*\(\s*\)\s*=>\s*\[`)

	for _, classMatch := range classMatches {
		varName := text[classMatch[2]:classMatch[3]]
		internalName := text[classMatch[4]:classMatch[5]]
		classStart := classMatch[0]

		// 找到类的结束位置（匹配大括号）
		classEnd := findClassEnd(text, classMatch[1]-1)
		if classEnd == -1 {
			continue
		}

		classBody := text[classStart:classEnd]

		// 在类体内查找 typeName
		typeMatch := typeNameRe.FindStringSubmatch(classBody)
		if typeMatch == nil {
			continue
		}
		typeName := typeMatch[1]

		// 在类体内查找 fields
		fieldsMatch := fieldsRe.FindStringIndex(classBody)
		if fieldsMatch == nil {
			continue
		}

		// 找到 fields 数组的开始位置
		bracketPos := classStart + fieldsMatch[1] - 1
		fields := extractFieldArray(text, bracketPos, diagnostics)

		pkg, shortName := parseTypeName(typeName)
		msg := Message{
			TypeName:     typeName,
			VarName:      varName,
			InternalName: internalName,
			Fields:       fields,
			Package:      pkg,
			ShortName:    shortName,
			Pos:          classStart,
			ModuleStart:  moduleStartForPos(moduleStarts, classStart),
		}
		messages = append(messages, msg)
	}

	// Pattern 2: transpiled/minified bundle style
	// Example:
	// i.runtime=n.proto3,i.typeName="agent.v1.McpArgs",i.fields=n.proto3.util.newFieldList(()=>[{...}]);
	assignmentRe := regexp.MustCompile(`([\w$]+)\.typeName\s*=\s*"([\w.]+)"\s*[,;]\s*[\w$]+\.fields\s*=\s*\w+(?:\.\w+)*\.util\.newFieldList\s*\(\s*\(\s*\)\s*=>\s*\[`)
	assignmentMatches := assignmentRe.FindAllStringSubmatchIndex(text, -1)

	// Build class declaration index for resolving InternalName.
	// Minified bundles use class declarations (not class expressions):
	//   class T extends s.Message{constructor(){this.typeName="agent.v1.AgentClientMessage";...}}
	// The export map references the class name (T), not "this".
	classDeclRe := regexp.MustCompile(`class\s+([\w$]+)\s+extends\s+[\w$.]+\s*{`)
	classDeclIdxs := classDeclRe.FindAllStringSubmatchIndex(text, -1)
	type classDeclInfo struct {
		name string
		pos  int
	}
	classDecls := make([]classDeclInfo, 0, len(classDeclIdxs))
	for _, cd := range classDeclIdxs {
		classDecls = append(classDecls, classDeclInfo{name: text[cd[2]:cd[3]], pos: cd[0]})
	}

	findEnclosingClassName := func(pos int) string {
		var best classDeclInfo
		bestDist := -1
		for _, cd := range classDecls {
			if cd.pos < pos {
				dist := pos - cd.pos
				if bestDist < 0 || dist < bestDist {
					bestDist = dist
					best = cd
				}
			}
		}
		if bestDist > 0 {
			return best.name
		}
		return ""
	}

	for _, m := range assignmentMatches {
		varName := text[m[2]:m[3]]
		typeName := text[m[4]:m[5]]

		// Skip duplicates already captured by class-body style
		alreadyExists := false
		for _, existing := range messages {
			if existing.TypeName == typeName && existing.VarName == varName {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			continue
		}

		// Locate array start from the regex end (which stops right before '[')
		start := m[1] - 1
		if start < 0 || start >= len(text) || text[start] != '[' {
			continue
		}
		fields := extractFieldArray(text, start, diagnostics)

		// Find enclosing class name for webpack export map resolution.
		internalName := findEnclosingClassName(m[0])

		pkg, shortName := parseTypeName(typeName)
		messages = append(messages, Message{
			TypeName:     typeName,
			VarName:      varName,
			InternalName: internalName,
			Fields:       fields,
			Package:      pkg,
			ShortName:    shortName,
			Pos:          m[0],
			ModuleStart:  moduleStartForPos(moduleStarts, m[0]),
		})
	}

	return messages
}

// findClassEnd finds the matching closing brace for a class definition
func findClassEnd(text string, openBrace int) int {
	depth := 0
	for i := openBrace; i < len(text); i++ {
		if text[i] == '{' {
			depth++
		} else if text[i] == '}' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func extractFieldArray(text string, start int, diagnostics *extractionDiagnostics) []Field {
	// Find matching bracket
	depth := 0
	end := start
	for i := start; i < len(text); i++ {
		if text[i] == '[' {
			depth++
		} else if text[i] == ']' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}

	arrayText := text[start:end]

	// Parse individual field objects by extracting each {...} block
	var fields []Field

	// Find each field object
	fieldObjects := extractFieldObjects(arrayText)

	for _, fieldObj := range fieldObjects {
		field, parseErr := parseFieldObject(fieldObj)
		if parseErr != nil {
			diagnostics.addSkippedField(fieldObj, parseErr)
			continue
		}
		diagnostics.addParsedField()
		fields = append(fields, *field)
	}

	return fields
}

// extractFieldObjects extracts individual {...} objects from array text
func extractFieldObjects(arrayText string) []string {
	var objects []string
	depth := 0
	start := -1

	for i := 0; i < len(arrayText); i++ {
		if arrayText[i] == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if arrayText[i] == '}' {
			depth--
			if depth == 0 && start >= 0 {
				objects = append(objects, arrayText[start:i+1])
				start = -1
			}
		}
	}

	return objects
}

// parseFieldObject parses a single field object like { no: 1, name: "foo", kind: "scalar", T: 9, opt: !0 }
func parseFieldObject(obj string) (*Field, error) {
	// Extract no
	noMatch := noRe.FindStringSubmatch(obj)
	if noMatch == nil {
		return nil, errors.New("missing field no")
	}
	no, _ := strconv.Atoi(noMatch[1])

	// Extract name
	nameMatch := nameRe.FindStringSubmatch(obj)
	if nameMatch == nil {
		return nil, errors.New("missing field name")
	}
	name := strings.TrimSpace(nameMatch[1])
	if !fieldNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid field name: %s", name)
	}

	// Extract kind
	kindMatch := kindRe.FindStringSubmatch(obj)
	if kindMatch == nil {
		return nil, errors.New("missing field kind")
	}
	kind := strings.TrimSpace(kindMatch[1])

	field := &Field{
		No:   no,
		Name: name,
		Kind: kind,
	}

	// Extract T (type) - can be:
	// 1. number (scalar): T: 9
	// 2. variable name: T: SPe
	// 3. getEnumType call: T: n.getEnumType(SPe) or T: n.proto3.getEnumType(SPe)

	// Try getEnumType pattern first (for enums)
	if enumMatch := enumTypeRe.FindStringSubmatch(obj); enumMatch != nil {
		field.T = enumMatch[1]
	} else {
		// Try simple T: value pattern
		if tMatch := tRe.FindStringSubmatch(obj); tMatch != nil {
			if t, err := strconv.Atoi(tMatch[1]); err == nil {
				field.T = t
			} else {
				field.T = tMatch[1]
			}
		}
	}

	// Check for oneof (within THIS object only)
	if oneofMatch := oneofRe.FindStringSubmatch(obj); oneofMatch != nil {
		candidate := strings.TrimSpace(oneofMatch[1])
		if oneofNameRe.MatchString(candidate) {
			field.Oneof = candidate
		}
	}

	// Check for repeated (within THIS object only)
	// !0 means true in minified JS
	if repeatedRe.MatchString(obj) {
		field.Repeated = true
	}

	// Check for optional (within THIS object only)
	if optRe.MatchString(obj) {
		field.Opt = true
	}

	// Check for map type: K: keyType, V: { kind: "scalar"|"message", T: valueType }
	if field.Kind == "map" {
		// Extract K (key type)
		if keyMatch := keyRe.FindStringSubmatch(obj); keyMatch != nil {
			field.MapKey, _ = strconv.Atoi(keyMatch[1])
		}

		// Extract V (value type) - property order can vary.
		if valueMatch := mapValueRe.FindStringSubmatch(obj); valueMatch != nil {
			valueObj := valueMatch[1]
			if kindMatch := mapValueKRe.FindStringSubmatch(valueObj); kindMatch != nil {
				field.MapValueKind = kindMatch[1]
			}
			if tMatch := mapValueTRe.FindStringSubmatch(valueObj); tMatch != nil {
				if t, err := strconv.Atoi(tMatch[1]); err == nil {
					field.MapValueT = t
				} else {
					field.MapValueT = tMatch[1]
				}
			}
		}
	}

	return field, nil
}

func extractEnums(text string, moduleStarts []int) []Enum {
	var enums []Enum

	// Pattern for enum: setEnumType(XXX, "xxx.v1.EnumName", [...]) (any package)
	// JS 变量名可以包含 $ 符号
	enumRe := regexp.MustCompile(`setEnumType\s*\(\s*([\w$]+)\s*,\s*"([\w.]+)"\s*,\s*\[`)

	matches := enumRe.FindAllStringSubmatchIndex(text, -1)
	for _, match := range matches {
		varName := text[match[2]:match[3]]
		typeName := text[match[4]:match[5]]

		// Extract enum values array
		bracketStart := match[1] - 1
		values := extractEnumValues(text, bracketStart)

		pkg, shortName := parseTypeName(typeName)
		enum := Enum{
			TypeName:    typeName,
			VarName:     varName,
			Values:      values,
			Package:     pkg,
			ShortName:   shortName,
			Pos:         match[0],
			ModuleStart: moduleStartForPos(moduleStarts, match[0]),
		}
		enums = append(enums, enum)
	}

	return enums
}

func extractServices(text string, moduleStarts []int) []Service {
	var services []Service

	// Pattern: VarName = { typeName: "xxx.v1.ServiceName", methods: { ... } }
	// Service definitions are object literals, not classes
	serviceRe := regexp.MustCompile(`([\w$]+)\s*=\s*\{\s*typeName:\s*"([\w.]+)"\s*,\s*methods:\s*\{`)

	matches := serviceRe.FindAllStringSubmatchIndex(text, -1)
	for _, match := range matches {
		varName := text[match[2]:match[3]]
		typeName := text[match[4]:match[5]]

		// Find the end of the methods object
		methodsStart := match[1] - 1 // position of '{'
		methodsEnd := findMatchingBrace(text, methodsStart)
		if methodsEnd == -1 {
			continue
		}

		methodsText := text[methodsStart:methodsEnd]
		methods := extractMethods(methodsText)

		pkg, shortName := parseTypeName(typeName)
		service := Service{
			TypeName:    typeName,
			VarName:     varName,
			Methods:     methods,
			Package:     pkg,
			ShortName:   shortName,
			Pos:         match[0],
			ModuleStart: moduleStartForPos(moduleStarts, match[0]),
		}
		services = append(services, service)
	}

	return services
}

func extractMethods(methodsText string) []Method {
	var methods []Method

	// Pattern: methodName: { name: "MethodName", I: n.Input, O: n.Output, kind: s.MethodKind.Unary }
	methodRe := regexp.MustCompile(`\w+:\s*\{\s*name:\s*"([^"]+)"\s*,\s*I:\s*([\w$.]+)\s*,\s*O:\s*([\w$.]+)\s*,\s*kind:\s*[\w$.]+\.(Unary|ServerStreaming|ClientStreaming|BiDiStreaming)`)

	matches := methodRe.FindAllStringSubmatch(methodsText, -1)
	for _, m := range matches {
		method := Method{
			Name:       m[1],
			InputType:  m[2],
			OutputType: m[3],
			Kind:       m[4],
		}
		methods = append(methods, method)
	}

	return methods
}

func findMatchingBrace(text string, start int) int {
	depth := 0
	for i := start; i < len(text); i++ {
		if text[i] == '{' {
			depth++
		} else if text[i] == '}' {
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func extractEnumValues(text string, start int) []EnumValue {
	// Find matching bracket
	depth := 0
	end := start
	for i := start; i < len(text); i++ {
		if text[i] == '[' {
			depth++
		} else if text[i] == ']' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}

	arrayText := text[start:end]

	var values []EnumValue
	valueRe := regexp.MustCompile(`\{\s*no:\s*(\d+)\s*,\s*name:\s*"([^"]+)"`)

	matches := valueRe.FindAllStringSubmatch(arrayText, -1)
	for _, m := range matches {
		no, _ := strconv.Atoi(m[1])
		values = append(values, EnumValue{No: no, Name: m[2]})
	}

	return values
}

func generateProtos(
	messages []Message,
	enums []Enum,
	services []Service,
	resolver *TypeResolver,
	wpg *webpackModuleGraph,
	run *extractionRun,
	outputDir string,
) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Sort messages and enums for deterministic output
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Package != messages[j].Package {
			return messages[i].Package < messages[j].Package
		}
		return messages[i].ShortName < messages[j].ShortName
	})
	sort.Slice(enums, func(i, j int) bool {
		if enums[i].Package != enums[j].Package {
			return enums[i].Package < enums[j].Package
		}
		return enums[i].ShortName < enums[j].ShortName
	})
	sort.Slice(services, func(i, j int) bool {
		return services[i].TypeName < services[j].TypeName
	})

	// Group by package
	packages := make(map[string]struct {
		messages []Message
		enums    []Enum
		services []Service
	})

	for _, msg := range messages {
		pkg := packages[msg.Package]
		pkg.messages = append(pkg.messages, msg)
		packages[msg.Package] = pkg
	}

	for _, enum := range enums {
		pkg := packages[enum.Package]
		pkg.enums = append(pkg.enums, enum)
		packages[enum.Package] = pkg
	}

	for _, svc := range services {
		pkg := packages[svc.Package]
		pkg.services = append(pkg.services, svc)
		packages[svc.Package] = pkg
	}

	// Build global type maps for copying
	allMessages := make(map[string]*Message)
	allEnums := make(map[string]*Enum)

	for pkgName, pkg := range packages {
		if isGooglePkg(pkgName) {
			continue
		}
		for i := range pkg.messages {
			msg := &pkg.messages[i]
			allMessages[msg.TypeName] = msg
		}
		for i := range pkg.enums {
			enum := &pkg.enums[i]
			allEnums[enum.TypeName] = enum
		}
	}

	// Sort package names for deterministic generation order
	pkgNames := make([]string, 0, len(packages))
	for name := range packages {
		pkgNames = append(pkgNames, name)
	}
	sort.Strings(pkgNames)

	for _, pkgName := range pkgNames {
		pkg := packages[pkgName]
		// Skip Google standard packages - use official proto files instead
		// Also skip empty package names
		if isGooglePkg(pkgName) || pkgName == "" {
			if pkgName == "" {
				fmt.Printf("跳过: (空包名) 有 %d messages, %d enums, %d services\n", len(pkg.messages), len(pkg.enums), len(pkg.services))
			} else {
				fmt.Printf("跳过: %s (使用官方 proto 文件)\n", pkgName)
			}
			continue
		}

		// Copy all external types referenced by this package
		augmentedPkg := copyAllExternalTypes(
			pkgName,
			pkg,
			resolver,
			allMessages,
			allEnums,
			wpg,
			run,
		)
		if err := generateProtoFile(
			pkgName,
			augmentedPkg.messages,
			augmentedPkg.enums,
			pkg.services,
			resolver,
			wpg,
			run,
			outputDir,
		); err != nil {
			return fmt.Errorf("generate %s proto file: %w", pkgName, err)
		}
	}
	return nil
}

// copyAllExternalTypes copies all externally referenced types into the current package
func copyAllExternalTypes(pkgName string, pkg struct {
	messages []Message
	enums    []Enum
	services []Service
}, resolver *TypeResolver, allMessages map[string]*Message, allEnums map[string]*Enum, wpg *webpackModuleGraph, run *extractionRun) struct {
	messages []Message
	enums    []Enum
	services []Service
} {
	if run.copiedTypes[pkgName] == nil {
		run.copiedTypes[pkgName] = make(map[string]string)
	}

	// Build set of types already in this package
	// Also record them in copiedTypes so resolveFieldTypeWithPkg can use local names
	localTypes := make(map[string]bool)
	for _, msg := range pkg.messages {
		localTypes[msg.ShortName] = true
		// Mark as "local" - empty string means original type in this package
		if run.copiedTypes[pkgName][msg.ShortName] == "" {
			run.copiedTypes[pkgName][msg.ShortName] = "local:" + msg.TypeName
		}
	}
	for _, enum := range pkg.enums {
		localTypes[enum.ShortName] = true
		if run.copiedTypes[pkgName][enum.ShortName] == "" {
			run.copiedTypes[pkgName][enum.ShortName] = "local:" + enum.TypeName
		}
	}

	// Result starts with original types
	result := struct {
		messages []Message
		enums    []Enum
		services []Service
	}{
		messages: append([]Message{}, pkg.messages...),
		enums:    append([]Enum{}, pkg.enums...),
		services: pkg.services,
	}

	totalCopied := 0

	// Iterate until no new types need to be copied
	for round := 1; ; round++ {
		// Collect all external type references from current messages
		neededTypes := make(map[string]bool)

		for _, msg := range result.messages {
			sourcePkg := packageForTypeName(msg.TypeName, msg.Package)
			for _, f := range msg.Fields {
				collectFieldRefsSimple(f, pkgName, sourcePkg, msg.Pos, msg.ModuleStart, resolver, neededTypes, localTypes, wpg)
			}
		}
		for _, svc := range result.services {
			for _, m := range svc.Methods {
				collectMethodRefsSimple(m.InputType, pkgName, svc.Pos, svc.ModuleStart, resolver, neededTypes, localTypes, wpg)
				collectMethodRefsSimple(m.OutputType, pkgName, svc.Pos, svc.ModuleStart, resolver, neededTypes, localTypes, wpg)
			}
		}

		// Copy needed types (sorted for deterministic output)
		copiedThisRound := 0
		sortedNeeded := make([]string, 0, len(neededTypes))
		for tn := range neededTypes {
			sortedNeeded = append(sortedNeeded, tn)
		}
		sort.Strings(sortedNeeded)
		for _, typeName := range sortedNeeded {
			refPkg, shortName := parseTypeName(typeName)
			if refPkg == pkgName || isGooglePkg(refPkg) {
				continue
			}

			// Check if already local
			if localTypes[shortName] {
				continue
			}

			// Copy message
			if msg, ok := allMessages[typeName]; ok {
				msgCopy := *msg
				msgCopy.Package = pkgName
				// Keep original TypeName for source reference in comments
				// msgCopy.TypeName will be used for reference, store original separately
				result.messages = append(result.messages, msgCopy)
				run.copiedTypes[pkgName][shortName] = typeName // original full type name
				localTypes[shortName] = true
				copiedThisRound++
				fmt.Printf("  [%s] 轮%d 复制: %s\n", pkgName, round, typeName)
			} else if enum, ok := allEnums[typeName]; ok {
				// Copy enum
				enumCopy := *enum
				enumCopy.Package = pkgName
				result.enums = append(result.enums, enumCopy)
				run.copiedTypes[pkgName][shortName] = typeName
				localTypes[shortName] = true
				copiedThisRound++
				fmt.Printf("  [%s] 轮%d 复制枚举: %s\n", pkgName, round, typeName)
			} else {
				// Type not found - add to copiedTypes anyway to use local reference
				// This handles cases where the type exists locally but wasn't in our extraction
				run.copiedTypes[pkgName][shortName] = typeName
				localTypes[shortName] = true
				fmt.Printf("  [%s] 轮%d 警告: 类型未找到 %s，标记为本地引用\n", pkgName, round, typeName)
			}
		}

		totalCopied += copiedThisRound

		if copiedThisRound == 0 {
			break // No more types to copy
		}

		if round > 20 {
			fmt.Printf("  [%s] 警告: 复制轮次超过20，可能存在问题\n", pkgName)
			break
		}
	}

	if totalCopied > 0 {
		fmt.Printf("  [%s] 共复制 %d 个外部类型\n", pkgName, totalCopied)
	}

	// Sort result collections for deterministic output
	sort.Slice(result.messages, func(i, j int) bool {
		return result.messages[i].ShortName < result.messages[j].ShortName
	})
	sort.Slice(result.enums, func(i, j int) bool {
		return result.enums[i].ShortName < result.enums[j].ShortName
	})

	return result
}

// collectFieldRefsSimple collects external type references from a field (non-recursive, just this field)
func collectFieldRefsSimple(f Field, outputPkg string, sourcePkg string, contextPos int, contextModuleStart int, resolver *TypeResolver,
	neededTypes map[string]bool, localTypes map[string]bool, wpg *webpackModuleGraph) {

	type refWithKind struct {
		ref  string
		kind string
	}

	var refs []refWithKind
	if f.Kind == "message" || f.Kind == "enum" {
		if v, ok := f.T.(string); ok {
			refs = append(refs, refWithKind{ref: v, kind: f.Kind})
		}
	}
	if f.Kind == "map" && (f.MapValueKind == "message" || f.MapValueKind == "enum") {
		if v, ok := f.MapValueT.(string); ok {
			refs = append(refs, refWithKind{ref: v, kind: f.MapValueKind})
		}
	}

	for _, item := range refs {
		typeName, err := resolveTypeNameWithGraph(item.ref, contextPos, contextModuleStart, resolver, sourcePkg, item.kind, wpg)
		if err != nil {
			continue
		}

		refPkg, shortName := parseTypeName(typeName)
		if refPkg == "" || refPkg == outputPkg || isGooglePkg(refPkg) {
			continue
		}

		// Skip if already local
		if localTypes[shortName] {
			continue
		}

		neededTypes[typeName] = true
	}
}

// collectMethodRefsSimple collects external type references from a method type
func collectMethodRefsSimple(ref string, currentPkg string, contextPos int, contextModuleStart int, resolver *TypeResolver,
	neededTypes map[string]bool, localTypes map[string]bool, wpg *webpackModuleGraph) {

	typeName, err := resolveTypeNameWithGraph(ref, contextPos, contextModuleStart, resolver, currentPkg, "message", wpg)
	if err != nil {
		return
	}

	refPkg, shortName := parseTypeName(typeName)
	if refPkg == "" || refPkg == currentPkg || isGooglePkg(refPkg) {
		return
	}

	if localTypes[shortName] {
		return
	}

	neededTypes[typeName] = true
}

// TypeNode represents a node in the nested type tree
type TypeNode struct {
	Name     string
	Message  *Message
	Enum     *Enum
	Children map[string]*TypeNode
}

// collectImports collects only Google standard imports (all other types are copied locally)
func collectImports(currentPkg string, messages []Message, services []Service, resolver *TypeResolver, wpg *webpackModuleGraph) map[string]bool {
	imports := make(map[string]bool)

	addImport := func(ref string, contextPos int, contextModuleStart int, preferredPkg string, expectedKind string) {
		typeName, err := resolveTypeNameWithGraph(ref, contextPos, contextModuleStart, resolver, preferredPkg, expectedKind, wpg)
		if err != nil {
			return
		}

		refPkg, shortName := parseTypeName(typeName)
		// Only import Google standard types - all others are copied locally
		if refPkg == "google.protobuf" {
			var importFile string
			switch shortName {
			case "Struct", "Value", "ListValue", "NullValue":
				importFile = "google/protobuf/struct.proto"
			case "Timestamp":
				importFile = "google/protobuf/timestamp.proto"
			case "Duration":
				importFile = "google/protobuf/duration.proto"
			case "Any":
				importFile = "google/protobuf/any.proto"
			case "Empty":
				importFile = "google/protobuf/empty.proto"
			case "FieldMask":
				importFile = "google/protobuf/field_mask.proto"
			case "BoolValue", "BytesValue", "DoubleValue", "FloatValue",
				"Int32Value", "Int64Value", "StringValue", "UInt32Value", "UInt64Value":
				importFile = "google/protobuf/wrappers.proto"
			default:
				importFile = "google/protobuf/descriptor.proto"
			}
			imports[importFile] = true
		} else if refPkg == "google.rpc" {
			var importFile string
			switch shortName {
			case "Status":
				importFile = "google/rpc/status.proto"
			case "Code":
				importFile = "google/rpc/code.proto"
			default:
				importFile = "google/rpc/status.proto"
			}
			imports[importFile] = true
		}
	}

	for _, msg := range messages {
		sourcePkg := packageForTypeName(msg.TypeName, msg.Package)
		for _, f := range msg.Fields {
			if f.Kind == "message" || f.Kind == "enum" {
				if ref, ok := f.T.(string); ok {
					addImport(ref, msg.Pos, msg.ModuleStart, sourcePkg, f.Kind)
				}
			}
			// Also check map value types
			if f.Kind == "map" && (f.MapValueKind == "message" || f.MapValueKind == "enum") {
				if ref, ok := f.MapValueT.(string); ok {
					addImport(ref, msg.Pos, msg.ModuleStart, sourcePkg, f.MapValueKind)
				}
			}
		}
	}

	for _, svc := range services {
		sourcePkg := packageForTypeName(svc.TypeName, svc.Package)
		for _, m := range svc.Methods {
			addImport(m.InputType, svc.Pos, svc.ModuleStart, sourcePkg, "message")
			addImport(m.OutputType, svc.Pos, svc.ModuleStart, sourcePkg, "message")
		}
	}

	return imports
}

func generateProtoFile(
	pkgName string,
	messages []Message,
	enums []Enum,
	services []Service,
	resolver *TypeResolver,
	wpg *webpackModuleGraph,
	run *extractionRun,
	outputDir string,
) error {
	// First, collect all cross-package imports
	imports := collectImports(pkgName, messages, services, resolver, wpg)

	var sb strings.Builder

	sb.WriteString(`syntax = "proto3";` + "\n\n")
	sb.WriteString(fmt.Sprintf("package %s;\n\n", pkgName))

	// Write imports
	if len(imports) > 0 {
		sortedImports := make([]string, 0, len(imports))
		for imp := range imports {
			sortedImports = append(sortedImports, imp)
		}
		sort.Strings(sortedImports)
		for _, imp := range sortedImports {
			sb.WriteString(fmt.Sprintf("import \"%s\";\n", imp))
		}
		sb.WriteString("\n")
	}

	goPackagePath := strings.ReplaceAll(pkgName, ".", "/")
	goPackageName := strings.ReplaceAll(pkgName, ".", "")
	sb.WriteString(fmt.Sprintf(`option go_package = "react-admin/cursor-server/gen/%s;%s";`+"\n\n", goPackagePath, goPackageName))

	// Build type tree
	root := &TypeNode{Children: make(map[string]*TypeNode)}

	for i := range messages {
		msg := &messages[i]
		path := getNestedPath(msg.ShortName)
		insertMessage(root, path, msg)
	}

	for i := range enums {
		enum := &enums[i]
		path := getNestedPath(enum.ShortName)
		insertEnum(root, path, enum)
	}

	// Write all top-level types
	writeTypeTree(root, &sb, resolver, 0, pkgName, wpg, run)

	// Write services
	sort.Slice(services, func(i, j int) bool {
		return services[i].ShortName < services[j].ShortName
	})

	for _, svc := range services {
		// Write source comment for service
		sb.WriteString(fmt.Sprintf("// Source: %s (var: %s)\n", svc.TypeName, svc.VarName))
		sb.WriteString(fmt.Sprintf("service %s {\n", svc.ShortName))
		for _, m := range svc.Methods {
			inputType := resolveMethodType(m.InputType, resolver, pkgName, svc.Pos, svc.ModuleStart, wpg, run)
			outputType := resolveMethodType(m.OutputType, resolver, pkgName, svc.Pos, svc.ModuleStart, wpg, run)

			switch m.Kind {
			case "ServerStreaming":
				sb.WriteString(fmt.Sprintf("  rpc %s(%s) returns (stream %s) {}\n", m.Name, inputType, outputType))
			case "ClientStreaming":
				sb.WriteString(fmt.Sprintf("  rpc %s(stream %s) returns (%s) {}\n", m.Name, inputType, outputType))
			case "BiDiStreaming":
				sb.WriteString(fmt.Sprintf("  rpc %s(stream %s) returns (stream %s) {}\n", m.Name, inputType, outputType))
			default: // Unary
				sb.WriteString(fmt.Sprintf("  rpc %s(%s) returns (%s) {}\n", m.Name, inputType, outputType))
			}
		}
		sb.WriteString("}\n\n")
	}

	// Write to file - single flat directory
	fileName := strings.ReplaceAll(pkgName, ".", "_") + ".proto"
	filePath := filepath.Join(outputDir, fileName)

	if err := os.WriteFile(filePath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write proto file %s: %w", filePath, err)
	}
	fmt.Printf("Generated: %s (%d messages, %d enums, %d services)\n", filePath, len(messages), len(enums), len(services))
	return nil
}

func resolveMethodType(ref string, resolver *TypeResolver, currentPkg string, contextPos int, contextModuleStart int, wpg *webpackModuleGraph, run *extractionRun) string {
	typeName, err := resolveTypeNameWithGraph(ref, contextPos, contextModuleStart, resolver, currentPkg, "message", wpg)
	if err != nil {
		run.diagnostics.addUnresolvedType("method:" + ref)
		return fallbackTypeToken(ref)
	}

	refPkg, shortName := parseTypeName(typeName)
	if refPkg == currentPkg || refPkg == "" {
		return shortName
	}
	// Check if this type was copied to current package
	if copied := run.copiedTypes[currentPkg]; copied != nil {
		if _, isCopied := copied[shortName]; isCopied {
			return shortName
		}
	}
	return refPkg + "." + shortName
}

func insertMessage(node *TypeNode, path []string, msg *Message) {
	if len(path) == 0 {
		return
	}

	name := path[0]
	if node.Children == nil {
		node.Children = make(map[string]*TypeNode)
	}

	child, exists := node.Children[name]
	if !exists {
		child = &TypeNode{Name: name, Children: make(map[string]*TypeNode)}
		node.Children[name] = child
	}

	if len(path) == 1 {
		child.Message = msg
	} else {
		insertMessage(child, path[1:], msg)
	}
}

func insertEnum(node *TypeNode, path []string, enum *Enum) {
	if len(path) == 0 {
		return
	}

	name := path[0]
	if node.Children == nil {
		node.Children = make(map[string]*TypeNode)
	}

	child, exists := node.Children[name]
	if !exists {
		child = &TypeNode{Name: name, Children: make(map[string]*TypeNode)}
		node.Children[name] = child
	}

	if len(path) == 1 {
		child.Enum = enum
	} else {
		insertEnum(child, path[1:], enum)
	}
}

func writeTypeTree(node *TypeNode, sb *strings.Builder, resolver *TypeResolver, indent int, currentPkg string, wpg *webpackModuleGraph, run *extractionRun) {
	// Get sorted child names
	var names []string
	for name := range node.Children {
		names = append(names, name)
	}
	sort.Strings(names)

	indentStr := strings.Repeat("  ", indent)

	for _, name := range names {
		child := node.Children[name]

		if child.Enum != nil {
			// Check if this is a copied type
			originalType := ""
			if copied := run.copiedTypes[currentPkg]; copied != nil {
				if orig, ok := copied[child.Enum.ShortName]; ok {
					originalType = orig
				}
			}

			// Write source comment for enum
			if originalType != "" {
				sb.WriteString(fmt.Sprintf("%s// Copied from: %s (var: %s)\n", indentStr, originalType, child.Enum.VarName))
			} else {
				sb.WriteString(fmt.Sprintf("%s// Source: %s (var: %s)\n", indentStr, child.Enum.TypeName, child.Enum.VarName))
			}
			// Write enum
			sb.WriteString(fmt.Sprintf("%senum %s {\n", indentStr, name))
			for _, v := range child.Enum.Values {
				sb.WriteString(fmt.Sprintf("%s  %s = %d;\n", indentStr, v.Name, v.No))
			}
			sb.WriteString(fmt.Sprintf("%s}\n\n", indentStr))
		} else if child.Message != nil || len(child.Children) > 0 {
			// Write source comment for message
			if child.Message != nil {
				varInfo := child.Message.VarName
				if child.Message.InternalName != "" && child.Message.InternalName != child.Message.VarName {
					varInfo = fmt.Sprintf("%s, class: %s", child.Message.VarName, child.Message.InternalName)
				}

				// Check if this is a copied type
				originalType := ""
				if copied := run.copiedTypes[currentPkg]; copied != nil {
					if orig, ok := copied[child.Message.ShortName]; ok {
						originalType = orig
					}
				}

				if originalType != "" {
					sb.WriteString(fmt.Sprintf("%s// Copied from: %s (var: %s)\n", indentStr, originalType, varInfo))
				} else {
					sb.WriteString(fmt.Sprintf("%s// Source: %s (var: %s)\n", indentStr, child.Message.TypeName, varInfo))
				}
			}
			// Write message (even if just a container for nested types)
			sb.WriteString(fmt.Sprintf("%smessage %s {\n", indentStr, name))

			// Write nested types first
			writeTypeTree(child, sb, resolver, indent+1, currentPkg, wpg, run)

			// Write fields if this node has a message
			if child.Message != nil {
				writeMessageFields(child.Message, sb, resolver, indent+1, wpg, run)
			}

			sb.WriteString(fmt.Sprintf("%s}\n\n", indentStr))
		}
	}
}

func writeMessageFields(msg *Message, sb *strings.Builder, resolver *TypeResolver, indent int, wpg *webpackModuleGraph, run *extractionRun) {
	indentStr := strings.Repeat("  ", indent)

	// Get the current message's path prefix for relative type resolution
	msgPath := msg.ShortName
	currentPkg := msg.Package
	sourcePkg := packageForTypeName(msg.TypeName, currentPkg)

	// Group fields by oneof
	oneofGroups := make(map[string][]Field)
	var regularFields []Field

	for _, f := range msg.Fields {
		if f.Oneof != "" {
			oneofGroups[f.Oneof] = append(oneofGroups[f.Oneof], f)
		} else {
			regularFields = append(regularFields, f)
		}
	}

	// Write regular fields
	for _, f := range regularFields {
		fieldType := resolveFieldTypeWithPkg(f, resolver, msgPath, currentPkg, sourcePkg, msg.Pos, msg.ModuleStart, wpg, run)
		prefix := ""
		if f.Repeated {
			prefix = "repeated "
		} else if f.Opt {
			prefix = "optional "
		}
		sb.WriteString(fmt.Sprintf("%s%s%s %s = %d;\n", indentStr, prefix, fieldType, f.Name, f.No))
	}

	// Write oneof groups
	var oneofNames []string
	for name := range oneofGroups {
		oneofNames = append(oneofNames, name)
	}
	sort.Strings(oneofNames)

	for _, oneofName := range oneofNames {
		fields := oneofGroups[oneofName]
		sb.WriteString(fmt.Sprintf("%soneof %s {\n", indentStr, oneofName))
		for _, f := range fields {
			fieldType := resolveFieldTypeWithPkg(f, resolver, msgPath, currentPkg, sourcePkg, msg.Pos, msg.ModuleStart, wpg, run)
			sb.WriteString(fmt.Sprintf("%s  %s %s = %d;\n", indentStr, fieldType, f.Name, f.No))
		}
		sb.WriteString(fmt.Sprintf("%s}\n", indentStr))
	}
}

// parseTypeName extracts package and full nested path from type name
// "agent.v1.Foo" -> ("agent.v1", "Foo")
// "agent.v1.Foo.Bar" -> ("agent.v1", "Foo.Bar")
// "anyrun.v1.PodStatus" -> ("anyrun.v1", "PodStatus")
// "google.protobuf.Timestamp" -> ("google.protobuf", "Timestamp")
func parseTypeName(typeName string) (pkg, shortName string) {
	// Find pattern: xxx.v1.Rest or xxx.vN.Rest
	versionRe := regexp.MustCompile(`^([\w.]+\.v\d+)\.(.+)$`)
	if match := versionRe.FindStringSubmatch(typeName); match != nil {
		return match[1], match[2]
	}

	// Handle google.protobuf.XXX pattern
	if strings.HasPrefix(typeName, "google.protobuf.") {
		rest := strings.TrimPrefix(typeName, "google.protobuf.")
		return "google.protobuf", rest
	}

	// Handle google.rpc.XXX pattern
	if strings.HasPrefix(typeName, "google.rpc.") {
		rest := strings.TrimPrefix(typeName, "google.rpc.")
		return "google.rpc", rest
	}

	// Fallback: split at last dot
	parts := strings.Split(typeName, ".")
	if len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
	}
	return "", typeName
}

func packageForTypeName(typeName string, fallback string) string {
	pkg, _ := parseTypeName(typeName)
	if pkg == "" {
		return fallback
	}
	return pkg
}

// getNestedPath returns the path components for a nested type
// "Foo" -> ["Foo"]
// "Foo.Bar" -> ["Foo", "Bar"]
// "Foo.Bar.Baz" -> ["Foo", "Bar", "Baz"]
func getNestedPath(shortName string) []string {
	return strings.Split(shortName, ".")
}

func resolveFieldType(f Field, resolver *TypeResolver, contextPos int, contextModuleStart int, wpg *webpackModuleGraph, run *extractionRun) string {
	return resolveFieldTypeWithPkg(f, resolver, "", "", "", contextPos, contextModuleStart, wpg, run)
}

// resolveFieldTypeWithPkg resolves field type with package awareness
// parentPath is like "ConversationMessage" or "ConversationMessage.ToolResult"
// currentPkg is the package of the current message being written (e.g., "agent.v1")
func resolveFieldTypeWithPkg(f Field, resolver *TypeResolver, parentPath string, currentPkg string, sourcePkg string, contextPos int, contextModuleStart int, wpg *webpackModuleGraph, run *extractionRun) string {
	resolveNamedType := func(ref string, expectedKind string) string {
		typeName, err := resolveTypeNameWithGraph(ref, contextPos, contextModuleStart, resolver, sourcePkg, expectedKind, wpg)
		if err != nil {
			run.diagnostics.addUnresolvedType(expectedKind + ":" + ref)
			return fallbackTypeToken(ref)
		}

		refPkg, shortName := parseTypeName(typeName)

		// If the type is nested under the same parent, use relative path
		if parentPath != "" && strings.HasPrefix(shortName, parentPath+".") {
			// ConversationMessage.CodeChunk -> CodeChunk (when inside ConversationMessage)
			return strings.TrimPrefix(shortName, parentPath+".")
		}

		// If same package, use short name only
		if refPkg == currentPkg || refPkg == "" {
			return shortName
		}

		// Check if this type was copied to current package (circular import resolution)
		if copied := run.copiedTypes[currentPkg]; copied != nil {
			if _, isCopied := copied[shortName]; isCopied {
				// This type exists locally as a copy, use short name
				return shortName
			}
		}

		// For cross-package references, use full type name
		return refPkg + "." + shortName
	}

	if f.Kind == "scalar" {
		if t, ok := f.T.(int); ok {
			return scalarTypes[t]
		}
		if t, ok := f.T.(float64); ok {
			return scalarTypes[int(t)]
		}
	}

	if f.Kind == "message" || f.Kind == "enum" {
		if ref, ok := f.T.(string); ok {
			return resolveNamedType(ref, f.Kind)
		}
	}

	if f.Kind == "map" {
		// Handle map types: map<KeyType, ValueType>
		keyType := scalarTypes[f.MapKey]
		if keyType == "" {
			keyType = "string" // default
		}

		var valueType string
		if f.MapValueKind == "scalar" {
			if t, ok := f.MapValueT.(int); ok {
				valueType = scalarTypes[t]
			} else if t, ok := f.MapValueT.(float64); ok {
				valueType = scalarTypes[int(t)]
			}
		} else if f.MapValueKind == "message" || f.MapValueKind == "enum" {
			if ref, ok := f.MapValueT.(string); ok {
				valueType = resolveNamedType(ref, f.MapValueKind)
			}
		}
		if valueType == "" {
			valueType = "bytes"
		}

		return fmt.Sprintf("map<%s, %s>", keyType, valueType)
	}

	return "bytes" // fallback
}
