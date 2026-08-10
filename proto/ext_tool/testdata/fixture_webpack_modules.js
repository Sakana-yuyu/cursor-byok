// Webpack module fixture: simulates real Cursor webpack bundle patterns.
// Tests qualified ref resolution through the module import/export graph:
//   var r=n(5801)  → namespace import from module 5801
//   n.d(t,{KS:()=>T,Oy:()=>k})  → export map
//   I:r.KS,O:r.Oy  → qualified ref → import → export → internal symbol → type
//
// Uses real webpack bundle structure: var e={moduleId:(e,t,n)=>{body},...}

var n = { util: { newFieldList: function(fn) { return fn(); } }, proto3: { util: { newFieldList: function(fn) { return fn(); } } } };
n.d = function(t, exports) { /* webpack define exports mock */ };
var s = { MethodKind: { Unary: 1, ServerStreaming: 2, ClientStreaming: 3, BiDiStreaming: 4 } };

// Webpack module bundle (real pattern: var e={moduleId:(e,t,n)=>{body},...})
var e = {

// Module 5801: exports agent message types via n.d
5801:(e,t,n)=>{
  "use strict";
  n.d(t,{KS:()=>K7,Oy:()=>O7});
  // K7 = AgentClientMessage (message)
  K7.typeName="agent.v1.AgentClientMessage",K7.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "msg_id", kind: "scalar", T: 9 },
  ]);
  // O7 = AgentServerMessage (message)
  O7.typeName="agent.v1.AgentServerMessage",O7.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "response", kind: "scalar", T: 9 },
  ]);

  // Also export an enum via n.d (for wrong-kind testing)
  n.d(t,{DiagSev:()=>DS});
  setEnumType(DS, "agent.v1.DiagnosticSeverity", [
    { no: 0, name: "DIAGNOSTIC_SEVERITY_UNSPECIFIED" },
    { no: 1, name: "DIAGNOSTIC_SEVERITY_ERROR" },
  ]);
},

// Module 7714: imports module 5801, defines AgentService with qualified refs
7714:(e,t,n)=>{
  "use strict";
  var r=n(5801);

  // AgentService using qualified refs I:r.KS, O:r.Oy
  // r.KS → module 5801 export KS → internal symbol K7 → AgentClientMessage (message)
  // r.Oy → module 5801 export Oy → internal symbol O7 → AgentServerMessage (message)
  AgSvc={typeName:"agent.v1.AgentService",methods:{
    run:{name:"Run",I:r.KS,O:r.Oy,kind:s.MethodKind.BiDiStreaming},
  }};
},

// Module 8814: cross-module oneof/field qualified ref test
8814:(e,t,n)=>{
  "use strict";
  n.d(t,{AMsg:()=>AM,BM:()=>BM});

  // AM = SomeMessage
  AM.typeName="agent.v1.SomeMessage",AM.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "basic", kind: "scalar", T: 9 },
  ]);

  // BM = OtherMessage
  BM.typeName="agent.v1.OtherMessage",BM.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "value", kind: "scalar", T: 12 },
  ]);
},

// Module 9900: service with cross-module qualified refs
9900:(e,t,n)=>{
  "use strict";
  var m=n(8814);

  // Service using qualified refs for method types
  WpSvc={typeName:"agent.v1.WebpackService",methods:{
    check:{name:"Check",I:m.AMsg,O:m.BM,kind:s.MethodKind.Unary},
  }};
}

};
