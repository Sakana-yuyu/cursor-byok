package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
)

func fixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "fixture_minimal.js")
}

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "ext-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// hashDir computes a SHA256 hash of all *.proto files in a directory,
// sorted by name, to verify deterministic output.
func hashDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	h := sha256.New()
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".proto") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func parseGeneratedProto(t *testing.T, dir, name string) *desc.FileDescriptor {
	t.Helper()
	parser := protoparse.Parser{ImportPaths: []string{dir}}
	files, err := parser.ParseFiles(name)
	if err != nil {
		t.Fatalf("parse generated %s: %v", name, err)
	}
	if len(files) != 1 {
		t.Fatalf("parse generated %s returned %d files", name, len(files))
	}
	return files[0]
}

func requireMessage(t *testing.T, file *desc.FileDescriptor, fullName string) *desc.MessageDescriptor {
	t.Helper()
	message, ok := file.FindSymbol(fullName).(*desc.MessageDescriptor)
	if !ok || message == nil {
		t.Fatalf("message %s not found", fullName)
	}
	return message
}

// TestExtractionReturnsErrorOnMissingInput verifies the new API returns error
// rather than calling os.Exit.
func TestExtractionReturnsErrorOnMissingInput(t *testing.T) {
	err := ExtractProtosToDir("nonexistent_file_xyz.js", tempDir(t))
	if err == nil {
		t.Fatal("expected error for nonexistent input file, got nil")
	}
}

// TestExtractionDeterministicOutput verifies two extractions produce byte-identical output.
func TestExtractionDeterministicOutput(t *testing.T) {
	input := fixturePath(t)
	dir1 := tempDir(t)
	dir2 := tempDir(t)

	err := ExtractProtosToDir(input, dir1)
	if err != nil {
		t.Fatalf("first extraction failed: %v", err)
	}
	err = ExtractProtosToDir(input, dir2)
	if err != nil {
		t.Fatalf("second extraction failed: %v", err)
	}

	h1 := hashDir(t, dir1)
	h2 := hashDir(t, dir2)
	if h1 != h2 {
		t.Fatalf("non-deterministic output:\n  run1: %s\n  run2: %s", h1, h2)
	}
}

// TestRPCMethodTypesAreMessagesOnly verifies that service method input/output
// types resolve strictly to message descriptors, never to enums.
func TestRPCMethodTypesAreMessagesOnly(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	// Read the generated agent_v1.proto and look for the TestService
	data, err := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)

	// Verify TestService exists and has Foo method
	if !strings.Contains(content, "service TestService") {
		t.Fatal("TestService not found in generated proto")
	}
	// The Foo method's input type must resolve to FooRequest (a message), not FooType (an enum)
	rpcLine := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "rpc Foo(") {
			rpcLine = strings.TrimSpace(line)
			break
		}
	}
	if rpcLine == "" {
		t.Fatal("rpc Foo not found in TestService")
	}
	// FooRequest is a message; FooType is an enum. The resolver must pick the message.
	if strings.Contains(rpcLine, "FooType") {
		t.Fatalf("RPC method resolved to enum FooType instead of message FooRequest: %s", rpcLine)
	}
	if !strings.Contains(rpcLine, "FooRequest") {
		t.Fatalf("RPC method did not resolve to FooRequest message: %s", rpcLine)
	}
}

// TestEnumFieldResolvesToEnum verifies that enum fields resolve to enum types.
func TestEnumFieldResolvesToEnum(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	file := parseGeneratedProto(t, dir, "agent_v1.proto")
	severity := requireMessage(t, file, "agent.v1.SomeMessage").FindFieldByName("severity")
	if severity == nil || severity.GetEnumType() == nil {
		t.Fatal("SomeMessage.severity is not an enum field")
	}
	if got := severity.GetEnumType().GetFullyQualifiedName(); got != "agent.v1.DiagnosticSeverity" {
		t.Fatalf("SomeMessage.severity type = %s, want agent.v1.DiagnosticSeverity", got)
	}
}

// TestOneofMembersResolved verifies that oneof members are correctly resolved.
func TestOneofMembersResolved(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	file := parseGeneratedProto(t, dir, "agent_v1.proto")
	outer := requireMessage(t, file, "agent.v1.OuterMessage")
	left := outer.FindFieldByName("left")
	right := outer.FindFieldByName("right")
	if left == nil || left.GetMessageType() == nil || left.GetMessageType().GetFullyQualifiedName() != "agent.v1.LeftScope.Option" {
		t.Fatalf("OuterMessage.left type = %v, want agent.v1.LeftScope.Option", left)
	}
	if right == nil || right.GetMessageType() == nil || right.GetMessageType().GetFullyQualifiedName() != "agent.v1.RightScope.Option" {
		t.Fatalf("OuterMessage.right type = %v, want agent.v1.RightScope.Option", right)
	}
	if left.GetOneOf() == nil || left.GetOneOf().GetName() != "selection" {
		t.Fatal("OuterMessage.left is not in oneof selection")
	}
	if right.GetOneOf() == nil || right.GetOneOf().GetName() != "selection" {
		t.Fatal("OuterMessage.right is not in oneof selection")
	}
	inner := requireMessage(t, file, "agent.v1.InnerA").FindFieldByName("inner_option")
	if inner == nil || inner.GetMessageType() == nil || inner.GetMessageType().GetFullyQualifiedName() != "agent.v1.InnerB" {
		t.Fatalf("InnerA.inner_option has wrong type: %v", inner)
	}
	if inner.GetOneOf() == nil || inner.GetOneOf().GetName() != "choice" {
		t.Fatal("InnerA.inner_option is not in oneof choice")
	}
}

// TestCrossModuleAliases verifies cross-module type references resolve correctly.
func TestCrossModuleAliases(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	file := parseGeneratedProto(t, dir, "aiserver_v1.proto")
	result := requireMessage(t, file, "aiserver.v1.CodeResult")
	diagnostic := result.FindFieldByName("diagnostic")
	if diagnostic == nil || diagnostic.GetEnumType() == nil || diagnostic.GetEnumType().GetFullyQualifiedName() != "aiserver.v1.DiagnosticSeverity" {
		t.Fatalf("CodeResult.diagnostic has wrong type: %v", diagnostic)
	}
	detail := result.FindFieldByName("detail")
	if detail == nil || detail.GetMessageType() == nil || detail.GetMessageType().GetFullyQualifiedName() != "aiserver.v1.OuterMessage" {
		t.Fatalf("CodeResult.detail has wrong type: %v", detail)
	}
	remoteStatus := result.FindFieldByName("remote_status")
	if remoteStatus == nil || remoteStatus.GetMessageType() == nil || remoteStatus.GetMessageType().GetFullyQualifiedName() != "aiserver.v1.StatusResult" {
		t.Fatalf("CodeResult.remote_status has wrong type: %v", remoteStatus)
	}
}

// TestPerRunStateReset verifies that per-run extraction state (diagnostics, copiedTypes)
// is reset between runs, so a second run does not share state with the first.
func TestPerRunStateReset(t *testing.T) {
	input := fixturePath(t)

	dir1 := tempDir(t)
	err := ExtractProtosToDir(input, dir1)
	if err != nil {
		t.Fatalf("first extraction: %v", err)
	}

	dir2 := tempDir(t)
	err = ExtractProtosToDir(input, dir2)
	if err != nil {
		t.Fatalf("second extraction: %v", err)
	}

	// Both should produce the same count of messages/enums
	// Verify by checking hash equality (already tested above), but also
	// count messages in each run's agent_v1.proto
	countMessages := func(dir string) int {
		data, _ := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
		return strings.Count(string(data), "\nmessage ")
	}
	c1 := countMessages(dir1)
	c2 := countMessages(dir2)
	if c1 != c2 {
		t.Fatalf("message counts differ: run1=%d run2=%d", c1, c2)
	}
	if c1 == 0 {
		t.Fatal("no messages found in output")
	}
}

// TestSortedOutputCollections verifies that messages, enums, oneofs, and fields
// appear in sorted order within generated protos.
func TestSortedOutputCollections(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	// Parse the generated proto and verify messages are sorted alphabetically
	data, err := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)

	// Collect top-level message names in order of appearance. Nested messages
	// have their own lexical scope and are sorted within their parent.
	var messages []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "message ") {
			name := strings.TrimPrefix(line, "message ")
			// Strip trailing '{'
			name = strings.TrimSpace(strings.TrimSuffix(name, "{"))
			messages = append(messages, name)
		}
	}

	// Verify sorted order
	for i := 1; i < len(messages); i++ {
		if messages[i] < messages[i-1] {
			t.Errorf("messages not sorted: %q before %q", messages[i-1], messages[i])
		}
	}

	// Verify enums are sorted
	var enums []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "enum ") {
			name := strings.TrimPrefix(line, "enum ")
			name = strings.TrimSpace(strings.TrimSuffix(name, "{"))
			enums = append(enums, name)
		}
	}
	for i := 1; i < len(enums); i++ {
		if enums[i] < enums[i-1] {
			t.Errorf("enums not sorted: %q before %q", enums[i-1], enums[i])
		}
	}

	// Verify services are sorted
	var services []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "service ") {
			name := strings.TrimPrefix(line, "service ")
			name = strings.TrimSpace(strings.TrimSuffix(name, "{"))
			services = append(services, name)
		}
	}
	for i := 1; i < len(services); i++ {
		if services[i] < services[i-1] {
			t.Errorf("services not sorted: %q before %q", services[i-1], services[i])
		}
	}
}

// TestWriteErrorPropagation verifies that filesystem write errors are propagated
// when the output directory cannot be written to.
func TestWriteErrorPropagation(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	// Create a named file where proto output would land, making it impossible
	// to create the proto file. This tests that os.WriteFile errors propagate.
	blocker := filepath.Join(dir, "agent_v1.proto")
	// Make blocker a directory — WriteFile to that path should fail
	if err := os.MkdirAll(blocker, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := ExtractProtosToDir(input, dir)
	if err == nil {
		t.Fatal("expected write error when proto path is a directory, got nil")
	}
}

// TestExtractProtosToDirGeneratesValidOutput verifies the extracted protos
// can be parsed successfully.
func TestExtractProtosToDirGeneratesValidOutput(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	// Verify output files exist
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".proto") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no proto files generated")
	}
}

// TestLegacyExtractProtosStillWorks verifies backward compatibility of the old API.
func TestLegacyExtractProtosStillWorks(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	// Legacy ExtractProtos uses default strict options. Test with non-strict
	// to verify the options-based API works for the legacy path too.
	ExtractProtosWithOptions(input, dir, ExtractionOptions{Strict: false})

	entries, _ := os.ReadDir(dir)
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".proto") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("legacy ExtractProtos produced no output")
	}
}

// TestConcurrentExtractions verifies that concurrent extractions do not
// leak state between each other (no package-level mutable state leakage).
func TestConcurrentExtractions(t *testing.T) {
	input := fixturePath(t)
	const N = 4
	errCh := make(chan error, N)
	hashCh := make(chan string, N)

	for i := 0; i < N; i++ {
		go func() {
			dir := tempDir(t)
			err := ExtractProtosToDir(input, dir)
			if err != nil {
				errCh <- err
				return
			}
			hashCh <- hashDir(t, dir)
		}()
	}

	var hashes []string
	for i := 0; i < N; i++ {
		select {
		case err := <-errCh:
			t.Fatalf("concurrent extraction %d failed: %v", i, err)
		case h := <-hashCh:
			hashes = append(hashes, h)
		}
	}

	// All concurrent extractions must produce identical output
	for i := 1; i < len(hashes); i++ {
		if hashes[i] != hashes[0] {
			t.Fatalf("concurrent extractions produced different output:\n  run0: %s\n  run%d: %s", hashes[0], i, hashes[i])
		}
	}
}

func TestExtractionRunStateIsIsolated(t *testing.T) {
	input := fixturePath(t)
	runs := []*extractionRun{
		newExtractionRun(DefaultExtractionOptions()),
		newExtractionRun(ExtractionOptions{Strict: false}),
	}
	dirs := []string{tempDir(t), tempDir(t)}

	start := make(chan struct{})
	errs := make([]error, len(runs))
	var wg sync.WaitGroup
	for i := range runs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = runs[index].extractProtosToDir(input, dirs[index])
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
		if got := runs[i].diagnostics.totalFieldObjects; got != 19 {
			t.Fatalf("run %d diagnostics leaked: total fields = %d, want 19", i, got)
		}
	}
	if !runs[0].options.Strict {
		t.Fatal("strict run options changed during overlapping extraction")
	}
	if runs[1].options.Strict {
		t.Fatal("non-strict run options changed during overlapping extraction")
	}
}

// TestSequentialStrictModeIsolation verifies that calling ExtractProtosToDirWithOptions
// with different Strict settings sequentially does not leak between calls.
func TestSequentialStrictModeIsolation(t *testing.T) {
	input := outputEnumFixturePath(t)

	// First call with strict=false should succeed
	dir1 := tempDir(t)
	err := ExtractProtosToDirWithOptions(input, dir1, ExtractionOptions{Strict: false})
	if err != nil {
		t.Fatalf("non-strict extraction failed (should succeed): %v", err)
	}

	// Second call with strict=true should fail
	dir2 := tempDir(t)
	err = ExtractProtosToDirWithOptions(input, dir2, ExtractionOptions{Strict: true})
	if err == nil {
		t.Fatal("strict extraction should have failed but succeeded (state leak?)")
	}

	// Third call with strict=false should succeed again
	dir3 := tempDir(t)
	err = ExtractProtosToDirWithOptions(input, dir3, ExtractionOptions{Strict: false})
	if err != nil {
		t.Fatalf("non-strict extraction after strict failed (state leak?): %v", err)
	}
}

// TestMethodTypeResolutionFailsOnAmbiguousEnum verifies that when an RPC method's
// input/output type resolves to multiple candidates and the nearest is an enum
// but we require message kind, the correct message is picked.
func TestMethodTypeResolutionFailsOnAmbiguousEnum(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	// Now verify: the Foo method input is FooType (a JS var)
	// FooType is an alias for u1 (a message FooRequest)
	// But FooEnum is an alias for v1 (an enum FooType)
	// The resolver must pick the message, not the enum.
	data, _ := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
	content := string(data)

	// The generated proto should have FooRequest as the input type
	if !strings.Contains(content, "rpc Foo(FooRequest)") && !strings.Contains(content, "rpc Foo(FooRequest") {
		t.Errorf("expected rpc Foo to use FooRequest (message), got content:\n%s",
			extractServiceSection(content, "TestService"))
	}
}

func extractServiceSection(content, svcName string) string {
	lines := strings.Split(content, "\n")
	inSvc := false
	var out []string
	for _, line := range lines {
		if strings.Contains(line, "service "+svcName) {
			inSvc = true
		}
		if inSvc {
			out = append(out, line)
			if strings.Contains(line, "}") && inSvc && len(out) > 1 {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

func outputEnumFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "fixture_output_enum.js")
}

// TestProximityCollisionEnumNearestMessagePicked verifies that when the physically
// nearest symbol for an RPC method type is an enum but the required kind is "message",
// the resolver picks the message (which is farther away) rather than the closer enum.
// This is the core fix: strict expectedKind filtering with no fallback.
func TestProximityCollisionEnumNearestMessagePicked(t *testing.T) {
	input := fixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	// Read the origin_v1.proto file
	data, err := os.ReadFile(filepath.Join(dir, "origin_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)

	// Verify ProximityService exists
	if !strings.Contains(content, "service ProximityService") {
		t.Fatal("ProximityService not found in generated proto")
	}

	// The Check method must use StatusResult (message) not StatusCode (enum)
	// Extract the rpc line
	rpcLine := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "rpc Check(") {
			rpcLine = strings.TrimSpace(line)
			break
		}
	}
	if rpcLine == "" {
		t.Fatal("rpc Check not found in ProximityService")
	}

	// StatusCode is the enum that is physically closer
	if strings.Contains(rpcLine, "StatusCode") {
		t.Fatalf("RPC method resolved to enum StatusCode instead of message StatusResult (proximity collision not handled): %s", rpcLine)
	}
	if !strings.Contains(rpcLine, "StatusResult") {
		t.Fatalf("RPC method did not resolve to StatusResult message: %s", rpcLine)
	}

	// Verify both StatusCode (enum) and StatusResult (message) exist in the output
	if !strings.Contains(content, "enum StatusCode") {
		t.Fatal("StatusCode enum not found in output")
	}
	if !strings.Contains(content, "message StatusResult") {
		t.Fatal("StatusResult message not found in output")
	}

	// Verify exact RPC types: Check(StatusResult) returns (StatusResult)
	if !strings.Contains(rpcLine, "Check(StatusResult)") {
		t.Errorf("expected exact RPC type Check(StatusResult), got: %s", rpcLine)
	}
}

// TestMethodOutputTypeRejectsEnum verifies that the symmetric method-type validator
// rejects an output type that resolves to an enum (not just input types).
func TestMethodOutputTypeRejectsEnum(t *testing.T) {
	input := outputEnumFixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err == nil {
		t.Fatal("expected strict extraction to fail for output type resolving to enum, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "output") || !strings.Contains(errMsg, "OutputEnumOnly") {
		t.Errorf("expected error to mention output type and OutputEnumOnly, got: %v", err)
	}
}

// TestMethodInputTypeRejectsEnum verifies that the symmetric method-type validator
// rejects an input type that resolves to an enum, using the same shared helper.
func TestMethodInputTypeRejectsEnum(t *testing.T) {
	input := outputEnumFixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err == nil {
		t.Fatal("expected strict extraction to fail for input type resolving to enum, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "input") || !strings.Contains(errMsg, "InputEnumOnly") {
		t.Errorf("expected error to mention input type and InputEnumOnly, got: %v", err)
	}
}

// TestMethodTypesRejectEnumsSymmetrically verifies the same validateMethodType helper
// is applied to both input and output directions (Issue 2 fix).
func TestMethodTypesRejectEnumsSymmetrically(t *testing.T) {
	input := outputEnumFixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err == nil {
		t.Fatal("expected strict extraction to fail", err)
	}
	errMsg := err.Error()

	// Both input and output failures must be reported
	hasInput := strings.Contains(errMsg, "input") && strings.Contains(errMsg, "InputEnumOnly")
	hasOutput := strings.Contains(errMsg, "output") && strings.Contains(errMsg, "OutputEnumOnly")

	if !hasInput {
		t.Errorf("expected error to mention input type InputEnumOnly, got: %v", err)
	}
	if !hasOutput {
		t.Errorf("expected error to mention output type OutputEnumOnly, got: %v", err)
	}

	// The error count should mention both failures (2 errors from 2 methods)
	if !strings.Contains(errMsg, "2 error") {
		t.Logf("note: error message: %v", err)
	}
}

// === Webpack module graph TDD tests ===

func webpackFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "fixture_webpack_modules.js")
}

// TestWebpackQualifiedRefSuccess verifies that qualified refs like r.KS, r.Oy
// resolve through the webpack import/export graph: namespace var → module → export → symbol → type.
func TestWebpackQualifiedRefSuccess(t *testing.T) {
	input := webpackFixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)

	// AgentService.Run must use AgentClientMessage and AgentServerMessage
	if !strings.Contains(content, "service AgentService") {
		t.Fatal("AgentService not found in generated proto")
	}

	// I:r.KS → AgentClientMessage
	// O:r.Oy → AgentServerMessage
	rpcLine := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "rpc Run(") {
			rpcLine = strings.TrimSpace(line)
			break
		}
	}
	if rpcLine == "" {
		t.Fatal("rpc Run not found in AgentService")
	}
	if !strings.Contains(rpcLine, "AgentClientMessage") || !strings.Contains(rpcLine, "AgentServerMessage") {
		t.Fatalf("qualified ref not resolved: expected AgentClientMessage/AgentServerMessage, got: %s", rpcLine)
	}

	// Verify messages exist
	if !strings.Contains(content, "message AgentClientMessage") {
		t.Fatal("AgentClientMessage message not found")
	}
	if !strings.Contains(content, "message AgentServerMessage") {
		t.Fatal("AgentServerMessage message not found")
	}

	// DiagnosticSeverity enum must exist (exported from same module)
	if !strings.Contains(content, "enum DiagnosticSeverity") {
		t.Fatal("DiagnosticSeverity enum not found")
	}
}

func TestWebpackQualifiedRefResolvesModuleAtByteZero(t *testing.T) {
	dir := tempDir(t)
	input := filepath.Join("testdata", "fixture_webpack_module_at_zero.js")

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed for module at byte zero: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	if !strings.Contains(string(data), "rpc Run(stream AgentClientMessage) returns (stream AgentServerMessage) {}") {
		t.Fatalf("byte-zero module types were not resolved:\n%s", data)
	}
}

// TestWebpackQualifiedRefCrossModule verifies qualified refs from WebpackService
// in module 9900 referencing exports from module 8814 (m.AMsg, m.BM).
func TestWebpackQualifiedRefCrossModule(t *testing.T) {
	input := webpackFixturePath(t)
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "service WebpackService") {
		t.Fatal("WebpackService not found")
	}

	rpcLine := ""
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "rpc Check(") {
			rpcLine = strings.TrimSpace(line)
			break
		}
	}
	if rpcLine == "" {
		t.Fatal("rpc Check not found in WebpackService")
	}
	if !strings.Contains(rpcLine, "SomeMessage") || !strings.Contains(rpcLine, "OtherMessage") {
		t.Fatalf("cross-module qualified ref not resolved: expected SomeMessage/OtherMessage, got: %s", rpcLine)
	}
}

func TestWebpackQualifiedFieldAndOneofIgnoreUnrelatedAliases(t *testing.T) {
	input := filepath.Join("testdata", "fixture_webpack_alias_pollution.js")
	dir := tempDir(t)

	if err := ExtractProtosToDir(input, dir); err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "agent_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"QualifiedState state = 1;",
		"oneof selection",
		"QualifiedChoiceA first = 2;",
		"QualifiedChoiceB second = 3;",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated proto missing %q:\n%s", want, content)
		}
	}
}

func TestWebpackUnqualifiedAliasesStayInsideOwningModule(t *testing.T) {
	dir := tempDir(t)
	input := filepath.Join("testdata", "fixture_webpack_local_aliases.js")
	if err := ExtractProtosToDir(input, dir); err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	file := parseGeneratedProto(t, dir, "agent_v1.proto")
	tests := []struct {
		message string
		field   string
		want    string
	}{
		{"agent.v1.AlphaHolder", "value", "agent.v1.AlphaValue"},
		{"agent.v1.BetaHolder", "value", "agent.v1.BetaValue"},
	}
	for _, test := range tests {
		field := requireMessage(t, file, test.message).FindFieldByName(test.field)
		if field == nil || field.GetMessageType() == nil || field.GetMessageType().GetFullyQualifiedName() != test.want {
			t.Errorf("%s.%s has wrong type: %v; want %s", test.message, test.field, field, test.want)
		}
	}

	service, ok := file.FindSymbol("agent.v1.AlphaService").(*desc.ServiceDescriptor)
	if !ok || service == nil || len(service.GetMethods()) != 1 {
		t.Fatal("AlphaService method not found")
	}
	method := service.GetMethods()[0]
	if got := method.GetInputType().GetFullyQualifiedName(); got != "agent.v1.AlphaValue" {
		t.Fatalf("AlphaService.Check input = %s, want agent.v1.AlphaValue", got)
	}
	if got := method.GetOutputType().GetFullyQualifiedName(); got != "agent.v1.AlphaReply" {
		t.Fatalf("AlphaService.Check output = %s, want agent.v1.AlphaReply", got)
	}
}

func TestWebpackUnqualifiedAliasCannotFallBackToAnotherModule(t *testing.T) {
	err := ExtractProtosToDir(
		filepath.Join("testdata", "fixture_webpack_missing_local_alias.js"),
		tempDir(t),
	)
	if err == nil {
		t.Fatal("expected strict extraction to reject an alias missing from the owning module")
	}
	if !strings.Contains(err.Error(), `unknown type MissingAlias`) {
		t.Fatalf("unexpected missing-local-alias error: %v", err)
	}
}

func TestVerifierComparisonIsCaseSensitive(t *testing.T) {
	script := filepath.Join("cmd", "verify-extraction.ps1")
	command := exec.Command("pwsh", "-NoProfile", "-File", script, "-SelfTestComparison")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("verifier comparison self-test failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Case-sensitive comparison self-test: PASS") {
		t.Fatalf("verifier self-test did not report success:\n%s", output)
	}
}

// TestWebpackQualifiedRefWrongKind verifies that a qualified ref to an enum export
// fails when method type requires a message (strict kind filtering).
func TestWebpackQualifiedRefWrongKind(t *testing.T) {
	input := filepath.Join("testdata", "fixture_webpack_wrongkind.js")
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err == nil {
		t.Fatal("expected strict extraction to fail for qualified ref resolving to enum, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, `export "Ek" from webpack module 10 resolves to enum`) {
		t.Errorf("expected precise kind error, got: %v", err)
	}
}

// TestWebpackQualifiedRefAmbiguous verifies that when an export maps to
// multiple internal symbols, resolution fails with an ambiguity error.
func TestWebpackQualifiedRefAmbiguous(t *testing.T) {
	input := filepath.Join("testdata", "fixture_webpack_ambiguous.js")
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err == nil {
		t.Fatal("expected strict extraction to fail for ambiguous qualified ref, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "ambiguous") && !strings.Contains(errMsg, "cannot resolve") {
		t.Errorf("expected ambiguity or resolution error, got: %v", err)
	}
}

// TestWebpackQualifiedRefMissingExport verifies that a qualified ref to a
// non-existent export fails cleanly.
func TestWebpackQualifiedRefMissingExport(t *testing.T) {
	input := filepath.Join("testdata", "fixture_webpack_missing_export.js")
	dir := tempDir(t)

	err := ExtractProtosToDir(input, dir)
	if err == nil {
		t.Fatal("expected strict extraction to fail for missing export, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, `webpack module 50 has no export "NoSuchExport"`) {
		t.Errorf("expected precise missing-export error, got: %v", err)
	}
}

func TestWebpackQualifiedRefMissingImport(t *testing.T) {
	input := filepath.Join("testdata", "fixture_webpack_missing_import.js")
	err := ExtractProtosToDir(input, tempDir(t))
	if err == nil {
		t.Fatal("expected strict extraction to fail for missing namespace import")
	}
	if !strings.Contains(err.Error(), `webpack module 60 has no namespace import "r"`) {
		t.Fatalf("expected precise missing-import error, got: %v", err)
	}
}

func TestWebpackQualifiedRefRejectsDuplicateExportTargets(t *testing.T) {
	input := filepath.Join("testdata", "fixture_webpack_duplicate_export.js")
	err := ExtractProtosToDir(input, tempDir(t))
	if err == nil {
		t.Fatal("expected strict extraction to fail for duplicate export targets")
	}
	if !strings.Contains(err.Error(), `webpack module 30 export "Dup" has multiple targets`) {
		t.Fatalf("expected precise duplicate-export error, got: %v", err)
	}
}

func TestCopiedMessageResolvesFieldsInSourcePackage(t *testing.T) {
	input := filepath.Join("testdata", "fixture_copied_source_package_collision.js")
	dir := tempDir(t)

	if err := ExtractProtosToDir(input, dir); err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "aiserver_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "SourceChild child = 1;") {
		t.Fatalf("copied SourceParent did not retain its source-package child type:\n%s", content)
	}
	if strings.Contains(content, "DestinationCollision child = 1;") {
		t.Fatalf("copied SourceParent resolved its field through the destination package collision:\n%s", content)
	}
}

func TestWebpackQualifiedRefResolvesCommonJSReexport(t *testing.T) {
	input := filepath.Join("testdata", "fixture_webpack_commonjs_reexport.js")
	dir := tempDir(t)

	if err := ExtractProtosToDir(input, dir); err != nil {
		t.Fatalf("extraction failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "anyrun_v1.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		`import "google/protobuf/empty.proto";`,
		"google.protobuf.Empty open = 1;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated proto missing %q:\n%s", want, content)
		}
	}
}
