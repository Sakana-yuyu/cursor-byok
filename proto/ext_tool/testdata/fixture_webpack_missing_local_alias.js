var n = { proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
var e = {
100:(e,t,n)=>{
  Foreign.typeName="agent.v1.ForeignValue",Foreign.fields=n.proto3.util.newFieldList(()=>[]);
  var MissingAlias=Foreign;
},
200:(e,t,n)=>{
  Holder.typeName="agent.v1.LocalHolder",Holder.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "message", T: MissingAlias },
  ]);
}
};
