5801:(e,t,n)=>{
  n.d(t,{KS:()=>K7,Oy:()=>O7});
  K7.typeName="agent.v1.AgentClientMessage",K7.fields=n.proto3.util.newFieldList(()=>[
    {no:1,name:"msg_id",kind:"scalar",T:9},
  ]);
  O7.typeName="agent.v1.AgentServerMessage",O7.fields=n.proto3.util.newFieldList(()=>[
    {no:1,name:"response",kind:"scalar",T:9},
  ]);
},
7714:(e,t,n)=>{
  var r=n(5801);
  AgSvc={typeName:"agent.v1.AgentService",methods:{
    run:{name:"Run",I:r.KS,O:r.Oy,kind:s.MethodKind.BiDiStreaming},
  }};
}
