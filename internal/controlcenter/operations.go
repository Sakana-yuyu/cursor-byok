package controlcenter

// PreparedOperation is the shared high-risk prepare DTO. Fields must not be
// extended by domain packages.
type PreparedOperation struct {
	OperationID       string   `json:"operationId"`
	ConfirmationToken string   `json:"confirmationToken"`
	ExpiresAtUnixMS   int64    `json:"expiresAtUnixMs"`
	ImpactCodes       []string `json:"impactCodes"`
	RollbackAvailable bool     `json:"rollbackAvailable"`
}

// OperationResult is the shared high-risk execute DTO. State is one of
// succeeded, failed, rolled_back, rollback_failed.
type OperationResult struct {
	OperationID      string `json:"operationId"`
	State            string `json:"state"`
	ErrorCode        string `json:"errorCode,omitempty"`
	Retryable        bool   `json:"retryable,omitempty"`
	RollbackState    string `json:"rollbackState,omitempty"`
	FinishedAtUnixMS int64  `json:"finishedAtUnixMs"`
}
