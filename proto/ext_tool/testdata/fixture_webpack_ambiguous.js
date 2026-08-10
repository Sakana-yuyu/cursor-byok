// Fixture: same export name maps to an internal symbol that has two type definitions.
// Dup() => X, but X is registered as both MsgA and MsgB — ambiguity at the type level.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
n.d = function(t, exports) {};
var s = { MethodKind: { Unary: 1 } };

// Module 30: exports Dup which points to X
// X is registered as BOTH MsgA and MsgB (two messages with same var name)
30:(e,t,n)=>{
  "use strict";
  n.d(t,{Dup:()=>X});
  // Register X as both MsgA and MsgB (creates two symbolDef entries for X)
  X.typeName="agent.v1.MsgA",X.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "a", kind: "scalar", T: 9 },
  ]);
  X.typeName="agent.v1.MsgB",X.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "b", kind: "scalar", T: 9 },
  ]);
  // Now X has two symbolDef entries (both as "message" kind) — ambiguous
},

// Module 40: imports module 30, uses r.Dup
40:(e,t,n)=>{
  "use strict";
  var r=n(30);
  Svc={typeName:"agent.v1.AmbiguousService",methods:{
    run:{name:"Run",I:r.Dup,O:r.Dup,kind:s.MethodKind.Unary},
  }};
};
