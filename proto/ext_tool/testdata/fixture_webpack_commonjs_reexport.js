// Webpack CommonJS barrels re-export protobuf modules with __exportStar.

var n = { proto3: { util: { newFieldList: function(fn) { return fn(); } } } };

var modules = {
10:(e,t,n)=>{
  Object.defineProperty(t,"__esModule",{value:!0});
  t.Empty=void 0;
  EmptyMessage.typeName="google.protobuf.Empty",EmptyMessage.fields=n.proto3.util.newFieldList(()=>[]);
  t.Empty=EmptyMessage;
},
20:function(e,t,n){
  var s=this&&this.__exportStar||function(e,t){};
  s(n(10),t);
},
30:(e,t,n)=>{
  var I=n(20);
  Holder.typeName="anyrun.v1.CommonJsHolder",Holder.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "open", kind: "message", T: I.Empty },
  ]);
}
};
