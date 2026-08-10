// A qualified reference without a namespace import must fail.

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
n.d = function(t, exports) {};
var s = { MethodKind: { Unary: 1 } };

var e = {
50:(e,t,n)=>{
  n.d(t,{Msg:()=>M});
  M.typeName="agent.v1.ImportedMessage",M.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "scalar", T: 9 },
  ]);
},
60:(e,t,n)=>{
  Svc={typeName:"agent.v1.MissingImportService",methods:{
    run:{name:"Run",I:r.Msg,O:r.Msg,kind:s.MethodKind.Unary},
  }};
}
};
