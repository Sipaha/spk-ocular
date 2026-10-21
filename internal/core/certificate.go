package core

// CertificateStatus contains controller-reported metadata, never a parsed Secret.
type CertificateStatus struct {
	Ref            Ref                    `json:"ref"`
	CapturedAt     int64                  `json:"capturedAt"`
	NotBefore      CertificateTime        `json:"notBefore"`
	NotAfter       CertificateTime        `json:"notAfter"`
	Renewal        CertificateTime        `json:"renewal"`
	Validity       string                 `json:"validity"`
	SecretName     string                 `json:"secretName"`
	Conditions     []CertificateCondition `json:"conditions"`
	RequestedNames []CertificateName      `json:"requestedNames"`
	Issuer         *Ref                   `json:"issuer,omitempty"`
	IssuerState    string                 `json:"issuerState"`
	Consumers      []Ref                  `json:"consumers"`
	ConsumerState  string                 `json:"consumerState"`
	Truncated      bool                   `json:"truncated"`
}
type CertificateTime struct {
	State string `json:"state"`
	Value string `json:"value,omitempty"`
}
type CertificateCondition struct {
	Type      string `json:"type"`
	Status    string `json:"status"`
	Freshness string `json:"freshness"`
	Reason    string `json:"reason,omitempty"`
}
type CertificateName struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
