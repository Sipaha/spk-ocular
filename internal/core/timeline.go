package core

// Timeline is an explicit Events API snapshot, not a persistent audit history.
type Timeline struct {
	Events     []TimelineEvent    `json:"events"`
	Resources  []TimelineResource `json:"resources"`
	Problems   []GraphProblem     `json:"problems"`
	Discovery  string             `json:"discovery"`
	Truncated  bool               `json:"truncated"`
	CapturedAt int64              `json:"capturedAt"`
}

// FirstAt/LastAt bound observations of a cumulative event series. They do not
// establish continuous failure duration or timestamps of individual occurrences.
type TimelineEvent struct {
	ID               string `json:"id"`
	Ref              Ref    `json:"ref"`
	Subject          Ref    `json:"subject"`
	SubjectKind      string `json:"subjectKind"`
	Openable         bool   `json:"openable"`
	Type             string `json:"type"`
	Reason           string `json:"reason"`
	Message          string `json:"message"`
	Count            int64  `json:"count"`
	FirstAt          int64  `json:"firstAt"`
	LastAt           int64  `json:"lastAt"`
	TimeFallback     bool   `json:"timeFallback"`
	MessageTruncated bool   `json:"messageTruncated"`
}

type TimelineResource struct {
	Ref       Ref    `json:"ref"`
	KindTitle string `json:"kindTitle"`
	OwnerUID  string `json:"ownerUid,omitempty"`
}
