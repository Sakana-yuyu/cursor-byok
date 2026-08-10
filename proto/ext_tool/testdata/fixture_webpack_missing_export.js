// Fixture: qualified ref to an export that doesn't exist in the target module.
// r.NoSuchExport — module 50 has no such export, must fail.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
n.d = function(t, exports) {};
var s = { MethodKind: { Unary: 1 } };

var e = {

// Module 50: exports only RealMsg
50:(e,t,n)=>{
  "use strict";
  n.d(t,{RealMsg:()=>RM});
  RM.typeName="agent.v1.RealMsg",RM.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "x", kind: "scalar", T: 9 },
  ]);
},

// Module 60: tries to use r.NoSuchExport (doesn't exist)
60:(e,t,n)=>{
  "use strict";
  var r=n(50);
  Svc={typeName:"agent.v1.MissingExportService",methods:{
    run:{name:"Run",I:r.NoSuchExport,O:r.RealMsg,kind:s.MethodKind.Unary},
  }};
}

};
