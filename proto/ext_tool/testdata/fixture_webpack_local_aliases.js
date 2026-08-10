var n = { proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
var s = { MethodKind: { Unary: 1 } };
var e = {
100:(e,t,n)=>{
  Alpha.typeName="agent.v1.AlphaValue",Alpha.fields=n.proto3.util.newFieldList(()=>[]);
  AlphaReplyVar.typeName="agent.v1.AlphaReply",AlphaReplyVar.fields=n.proto3.util.newFieldList(()=>[]);
  var SharedAlias=Alpha;
  var ReplyAlias=AlphaReplyVar;
  AlphaHolderVar.typeName="agent.v1.AlphaHolder",AlphaHolderVar.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "message", T: SharedAlias },
  ]);
  AlphaSvc={typeName:"agent.v1.AlphaService",methods:{
    check:{name:"Check",I:SharedAlias,O:ReplyAlias,kind:s.MethodKind.Unary},
  }};
},
200:(e,t,n)=>{
  Beta.typeName="agent.v1.BetaValue",Beta.fields=n.proto3.util.newFieldList(()=>[]);
  var SharedAlias=Beta;
  BetaHolderVar.typeName="agent.v1.BetaHolder",BetaHolderVar.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "message", T: SharedAlias },
  ]);
}
};
