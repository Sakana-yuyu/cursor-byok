// Fixture for testing symmetric input/output enum rejection.
// The method output type references an enum-only symbol; strict validation must reject it.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
var s = { MethodKind: { Unary: 1, ServerStreaming: 2, ClientStreaming: 3, BiDiStreaming: 4 } };

(function() {

// Enum only — no message shares this symbol
setEnumType(OutputEnumOnly, "internapi.v1.ErrorCode", [
  { no: 0, name: "ERROR_CODE_UNSPECIFIED" },
  { no: 1, name: "ERROR_CODE_TIMEOUT" },
]);

// Service method whose OUTPUT type references the enum-only symbol
// Input has a valid message; output does not. Both must be checked by the symmetric validator.
msgOk.typeName="internapi.v1.ValidInput",msgOk.fields=n.proto3.util.newFieldList(()=>[
  { no: 1, name: "payload", kind: "scalar", T: 12 },
]);

// Also define an enum-only symbol for the input direction
setEnumType(InputEnumOnly, "internapi.v1.InputErrorCode", [
  { no: 0, name: "INPUT_ERROR_UNSPECIFIED" },
]);

SvcOut={typeName:"internapi.v1.OutputEnumService",methods:{
fail_output:{name:"FailOutput",I:msgOk,O:OutputEnumOnly,kind:s.MethodKind.Unary},
fail_input:{name:"FailInput",I:InputEnumOnly,O:msgOk,kind:s.MethodKind.Unary},
}};

})();
