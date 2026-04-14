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
	Screenshot       string `json:"screenshot,omitempty"`        // page state after failure
	ScreenshotBefore string `json:"screenshot_before,omitempty"` // page state before the failed step
	PageHTML     string        `json:"page_html,omitempty"`      // page HTML at time of failure (for debugging)
	PageURL      string        `json:"page_url,omitempty"`       // page URL at time of failure
	PageElements []ElementInfo `json:"page_elements,omitempty"`  // similar elements on failure (for agent context)
	Escalated    bool   `json:"escalated,omitempty"`  // true if auto-escalated from headless to headed
	StatusCode   int    `json:"status_code,omitempty"` // HTTP status code of the last navigation

	// Eval results
	EvalsPassed int           `json:"evals_passed,omitempty"` // number of eval assertions that passed
	EvalsFailed int           `json:"evals_failed,omitempty"` // number of eval assertions that failed
	EvalErrors  []string      `json:"eval_errors,omitempty"`  // descriptions of failed assertions
}
