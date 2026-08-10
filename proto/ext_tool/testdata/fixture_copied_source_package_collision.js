// A copied message must resolve its fields in the source package, even when
// the same JavaScript symbol names an unrelated type in the output package.

var n = { proto3: { util: { newFieldList: function(fn) { return fn(); } } } };

(function() {
  SharedSymbol.typeName="agent.v1.SourceChild",SharedSymbol.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "source_value", kind: "scalar", T: 9 },
  ]);

  SourceParent.typeName="agent.v1.SourceParent",SourceParent.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "child", kind: "message", T: SharedSymbol },
  ]);
})();

(function() {
  SharedSymbol.typeName="aiserver.v1.DestinationCollision",SharedSymbol.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "destination_value", kind: "scalar", T: 9 },
  ]);

  DestinationRoot.typeName="aiserver.v1.DestinationRoot",DestinationRoot.fields=n.proto3.util.newFieldList(()=>[
    { no: 1, name: "source", kind: "message", T: SourceParent },
  ]);
})();
