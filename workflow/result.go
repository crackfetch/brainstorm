package workflow

// ActionResult is the structured output of running a workflow action.
// Designed for machine consumption — LLM agents parse this as JSON.
type ActionResult struct {
	OK           bool   `json:"ok"`
	Action       string `json:"action"`
	Steps        int    `json:"steps"`
	DurationMs   int64  `json:"duration_ms"`
	Download     string `json:"download,omitempty"`
	DownloadSize int64  `json:"download_size,omitempty"`
	Error        string `json:"error,omitempty"`
	FailedStep   int    `json:"failed_step,omitempty"`
	StepType     string `json:"step_type,omitempty"`
	Screenshot   string `json:"screenshot,omitempty"`
}
