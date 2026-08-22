package controlcenter

// ControlCenterDomainStatus is a non-secret domain snapshot for the overview.
type ControlCenterDomainStatus struct {
	State           string `json:"state"`
	Count           int    `json:"count,omitempty"`
	WarningCode     string `json:"warningCode,omitempty"`
	UpdatedAtUnixMS int64  `json:"updatedAtUnixMs,omitempty"`
}

// ControlCenterOverview is derived at read time and must not contain secrets.
type ControlCenterOverview struct {
	Accounts   ControlCenterDomainStatus `json:"accounts"`
	RequestLab ControlCenterDomainStatus `json:"requestLab"`
	Routing    ControlCenterDomainStatus `json:"routing"`
	Agents     ControlCenterDomainStatus `json:"agents"`
	Profiles   ControlCenterDomainStatus `json:"profiles"`
}
