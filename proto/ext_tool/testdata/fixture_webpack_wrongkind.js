// Fixture: qualified ref resolves to enum export, but method requires message.
// r.Ek → module 10 export Ek → ErrCode enum → must fail in strict mode.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
n.d = function(t, exports) {};
var s = { MethodKind: { Unary: 1 } };

var e = {

// Module 10: exports only an enum as Ek
10:(e,t,n)=>{
  "use strict";
  n.d(t,{Ek:()=>ErrCode});
  setEnumType(ErrCode, "agent.v1.ErrorCode", [
    { no: 0, name: "ERROR_CODE_UNSPECIFIED" },
  ]);
},

// Module 20: imports module 10, uses r.Ek as method I/O
20:(e,t,n)=>{
  "use strict";
  var r=n(10);
  Svc={typeName:"agent.v1.WrongKindService",methods:{
    fail:{name:"Fail",I:r.Ek,O:r.Ek,kind:s.MethodKind.Unary},
  }};
}

};
