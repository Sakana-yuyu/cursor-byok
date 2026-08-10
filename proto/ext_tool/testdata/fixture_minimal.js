// Minimal fixture for extractor tests.
// Simulates minified Cursor bundle patterns.
// Uses the actual JavaScript patterns that extractor.go recognizes.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
var s = { MethodKind: { Unary: 1, ServerStreaming: 2, ClientStreaming: 3, BiDiStreaming: 4 } };
var l = function(){};

// Module 0: agent.v1 package
(function() {

// --- Enums ---
// setEnumType(VarName, "typeName", [...])
setEnumType(DiagnosticSeverity, "agent.v1.DiagnosticSeverity", [
  { no: 0, name: "DIAGNOSTIC_SEVERITY_UNSPECIFIED" },
  { no: 1, name: "DIAGNOSTIC_SEVERITY_ERROR" },
  { no: 2, name: "DIAGNOSTIC_SEVERITY_WARNING" },
]);

// --- Messages with "transpiled" inline pattern ---
// VarName.typeName = "...", VarName.fields = n...util.newFieldList(()=>[...])
hU.typeName="agent.v1.SomeMessage",hU.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "severity", kind: "enum", T: n.getEnumType(DiagnosticSeverity), opt: !0 },
  { no: 2, name: "text", kind: "scalar", T: 9, opt: !0 },
]);

// An enum with similar short name to a message alias
setEnumType(e0_status, "agent.v1.Status", [
  { no: 0, name: "STATUS_UNSPECIFIED" },
  { no: 1, name: "STATUS_OK" },
  { no: 2, name: "STATUS_ERROR" },
]);

// Alias: StatusEnum points to the actual enum e0_status (not the message e0)
let StatusEnum = e0_status;

// Message with neighboring aliases
e0.typeName="agent.v1.StatusResult",e0.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "status", kind: "enum", T: n.getEnumType(StatusEnum) },
  { no: 2, name: "detail", kind: "scalar", T: 9, opt: !0 },
]);

// --- Message that is the "correct" FooRequest for RPC ---
// And enum that shares a nearby alias
u1.typeName="agent.v1.FooRequest",u1.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "query", kind: "scalar", T: 9 },
]);

// An enum with similar name nearby
setEnumType(v1, "agent.v1.FooType", [
  { no: 0, name: "FOO_TYPE_UNSPECIFIED" },
  { no: 1, name: "FOO_TYPE_A" },
]);

// Alias: FooType -> u1 (message), but also let FooEnum -> v1 (enum) nearby
let FooType = u1;
let FooEnum = v1;

// Message that should be the response type for RPC
z1.typeName="agent.v1.FooResponse",z1.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "result", kind: "scalar", T: 9 },
]);

// --- Service with RPC methods ---
// RPC method input "FooType" (an alias for u1 which is a message)
// must resolve to FooRequest message, NOT FooType enum.
ServiceA={typeName:"agent.v1.TestService",methods:{
foo:{name:"Foo",I:FooType,O:z1,kind:s.MethodKind.Unary},
}};

// --- Messages with oneof ---
p1.typeName="agent.v1.OuterMessage",p1.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "id", kind: "scalar", T: 9 },
  { no: 2, name: "left", kind: "message", T: LeftOption, oneof: "selection" },
  { no: 3, name: "right", kind: "message", T: RightOption, oneof: "selection" },
]);

// Repeated short name Option in distinct nested scopes.
LeftOption.typeName="agent.v1.LeftScope.Option",LeftOption.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "left_value", kind: "scalar", T: 9 },
]);
RightOption.typeName="agent.v1.RightScope.Option",RightOption.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "right_value", kind: "scalar", T: 12 },
]);

// Inner messages for oneof members
InnerA.typeName="agent.v1.InnerA",InnerA.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "x", kind: "scalar", T: 9 },
  { no: 2, name: "inner_option", kind: "message", T: InnerB, oneof: "choice" },
  { no: 3, name: "inner_other", kind: "scalar", T: 5, oneof: "choice" },
]);

InnerB.typeName="agent.v1.InnerB",InnerB.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "value", kind: "scalar", T: 12 },
]);

})();

// Module 1: aiserver.v1 package (cross-module)
(function() {

// Cross-module reference: CodeResult references agent.v1 DiagnosticSeverity
q2.typeName="aiserver.v1.CodeResult",q2.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "diagnostic", kind: "enum", T: n.getEnumType(DiagnosticSeverity) },
  { no: 2, name: "detail", kind: "message", T: p1 },
  { no: 3, name: "remote_status", kind: "message", T: RemoteStatus },
]);

// Cross-module alias
let RemoteStatus = e0;

})();

// Module 2: proximity collision test (origin.v1 package)
// The enum is physically closer to the service definition than the message,
// but the method requires a message. Strict kind resolution must pick the message.
(function() {

// Enum defined immediately before the service - physically closest
setEnumType(collisionVar, "origin.v1.StatusCode", [
  { no: 0, name: "STATUS_CODE_UNSPECIFIED" },
  { no: 1, name: "STATUS_CODE_OK" },
  { no: 2, name: "STATUS_CODE_ERROR" },
]);

// Service placed right after the enum, referencing collisionVar for method I/O
ProxSvc={typeName:"origin.v1.ProximityService",methods:{
check:{name:"Check",I:collisionVar,O:collisionVar,kind:s.MethodKind.Unary},
}};

// Message defined far from the service (larger position offset)
// collisionVar is made an alias for this message via the let-binding
farMsg.typeName="origin.v1.StatusResult",farMsg.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "data", kind: "scalar", T: 9 },
]);

// Alias: collisionVar also maps to farMsg (message) — creates the proximity collision.
// The enum definition is closer to ProxSvc, but with expectedKind="message",
// the resolver must pick farMsg (message), not StatusCode (enum).
let collisionVar = farMsg;

})();
