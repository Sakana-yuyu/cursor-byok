// Duplicate export declarations must be treated as ambiguous.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
n.d = function(t, exports) {};
var s = { MethodKind: { Unary: 1 } };

var e = {
30:(e,t,n)=>{
  n.d(t,{Dup:()=>X,Dup:()=>Y});
  X.typeName="agent.v1.DuplicateExportRequest",X.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "scalar", T: 9 },
  ]);
  Y.typeName="agent.v1.DuplicateExportResponse",Y.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "scalar", T: 12 },
  ]);
},
40:(e,t,n)=>{
  var r=n(30);
  Svc={typeName:"agent.v1.DuplicateExportService",methods:{
    run:{name:"Run",I:r.Dup,O:r.Dup,kind:s.MethodKind.Unary},
  }};
}
};
