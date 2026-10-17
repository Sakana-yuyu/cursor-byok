// 兼容本地仓库状态处理接口；保留完整旧响应状态和嵌套类型，不替换新版已存在的顶层定义。
message BatchRepositoryStatusRequest {
  repeated RepositoryStatusRequest requests = 1;
}

message BatchRepositoryStatusResponse {
  repeated RepositoryStatusResponse responses = 1;
}

message RepositoryStatusRequest {
  RepositoryInfo repository = 1;
}

message RepositoryStatusResponse {
  message AuthTokenNotAuthorized {
  }
  message AuthTokenNotFound {
  }
  message EmptyMessage {
  }
  message NotFound {
  }
  message NotSubscribed {
  }
  message Synced {
    string branch = 1;
    string commit = 2;
  }
  message Syncing {
    string branch = 1;
    string old_commit = 2;
    string new_commit = 3;
    float progress = 4;
  }
  message TooBig {
    int32 max_size = 1;
  }
  message Uploading {
    float progress = 1;
  }

  optional bool is_owner = 9;
  oneof status {
    NotFound not_found = 1;
    Uploading uploading = 2;
    Syncing syncing = 3;
    Synced synced = 4;
    NotSubscribed not_subscribed = 5;
    TooBig too_big = 6;
    AuthTokenNotFound auth_token_not_found = 7;
    AuthTokenNotAuthorized auth_token_not_authorized = 8;
    EmptyMessage error_uploading = 10;
    EmptyMessage error_syncing = 11;
  }
}
