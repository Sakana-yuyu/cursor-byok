// Qualified fields must resolve through the target module's exact internal symbols.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); }, getEnumType: function(value) { return value; } } } };
n.d = function(t, exports) {};

var e = {
100:(e,t,n)=>{
  n.d(t,{Choice$A:()=>A,ChoiceB:()=>B,State:()=>E});
  A.typeName="agent.v1.QualifiedChoiceA",A.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "scalar", T: 9 },
  ]);
  B.typeName="agent.v1.QualifiedChoiceB",B.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "scalar", T: 12 },
  ]);
  Wrong.typeName="agent.v1.WrongQualifiedChoice",Wrong.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "wrong", kind: "scalar", T: 8 },
  ]);
  setEnumType(E, "agent.v1.QualifiedState", [
    { no: 0, name: "QUALIFIED_STATE_UNSPECIFIED" },
    { no: 1, name: "QUALIFIED_STATE_READY" },
  ]);
},
200:(e,t,n)=>{
  var q=n(100);
  Holder.typeName="agent.v1.QualifiedHolder",Holder.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "state", kind: "enum", T: n.proto3.getEnumType(q.State) },
    { no: 2, name: "first", kind: "message", T: q.Choice$A, oneof: "selection" },
    { no: 3, name: "second", kind: "message", T: q.ChoiceB, oneof: "selection" },
  ]);
},
300:(e,t,n)=>{
  // This unrelated alias used to pollute every module's symbol table.
  var A=Wrong;
}
};
