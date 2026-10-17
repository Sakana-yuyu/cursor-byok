// 兼容旧持久化会话的显式类型集合；来自升级前已使用的编译输入，只有顶层类型缺失时补入。
message AgentConversationTurn {
  UserMessage user_message = 1;
  repeated ConversationStep steps = 2;
  optional string request_id = 3;
}

message AgentConversationTurnStructure {
  bytes user_message = 1;
  repeated bytes steps = 2;
  optional string request_id = 3;
  optional string encrypted_model = 4;
  optional uint32 dynamic_tool_count = 5;
  repeated uint32 send_message_step_indices = 6;
}

message ConversationState {
  repeated string root_prompt_messages_json = 1;
  repeated ConversationTurn turns = 8;
  repeated TodoItem todos = 3;
  repeated string pending_tool_calls = 4;
  ConversationTokenDetails token_details = 5;
  optional ConversationSummary summary = 6;
  optional ConversationPlan plan = 7;
  optional ConversationSummaryArchive summary_archive = 9;
  map<string, FileState> file_states = 10;
  repeated ConversationSummaryArchive summary_archives = 11;
  map<string, PlanRegistryEntry> plans = 12;
  repeated CommunicateUpdateHistoryEntry communicate_update_history = 13;
  optional string communicate_update_final_summary = 14;
  optional string communicate_update_completed_subtitle = 15;
  map<string, CommunicateUpdateTurnState> communicate_update_states_by_parent_tool_call_id = 16;
}

message ConversationSummary {
  string summary = 1;
}

message ConversationTurn {
  oneof turn {
    AgentConversationTurn agent_conversation_turn = 1;
    ShellConversationTurn shell_conversation_turn = 2;
  }
}

message ConversationTurnStructure {
  oneof turn {
    AgentConversationTurnStructure agent_conversation_turn = 1;
    ShellConversationTurnStructure shell_conversation_turn = 2;
  }
}

message ConversationSummaryArchive {
  repeated bytes summarized_messages = 1;
  string summary = 2;
  uint32 window_tail = 3;
  bytes summary_message = 4;
}

message ShellConversationTurn {
  ShellCommand shell_command = 1;
  ShellOutput shell_output = 2;
}

message ShellConversationTurnStructure {
  bytes shell_command = 1;
  bytes shell_output = 2;
}

message ShellOutput {
  string stdout = 1;
  string stderr = 2;
  int32 exit_code = 3;
}
