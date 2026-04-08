package workflow

import "time"

// Workflow defines a complete browser automation workflow loaded from YAML.
type Workflow struct {
	Name    string            `yaml:"name"`
	Env     map[string]string `yaml:"env,omitempty"`
	Actions map[string]Action `yaml:"actions"`
}

// Action is a named sequence of steps with an optional starting URL.
type Action struct {
	URL    string       `yaml:"url,omitempty"`
	Headed bool         `yaml:"headed,omitempty"` // show browser window (used by BRZ_HEADED=auto)
	Steps  []Step       `yaml:"steps"`
	Eval   []EvalAssert `yaml:"eval,omitempty"` // post-action assertions
}

// EvalAssert is a single post-action assertion. Exactly one field should be set.
type EvalAssert struct {
	// Page state
	JS          string `yaml:"js,omitempty"`           // JS expression that must return truthy
	URLContains string `yaml:"url_contains,omitempty"` // current URL must contain this string
	TextVisible string `yaml:"text_visible,omitempty"` // text must be visible on page
	NoText      string `yaml:"no_text,omitempty"`      // text must NOT be visible on page
	Selector    string `yaml:"selector,omitempty"`      // element must exist on page
	Timeout     string `yaml:"timeout,omitempty"`       // timeout for page-state checks (default "5s")

	// Response state
	StatusCode int `yaml:"status_code,omitempty"` // HTTP status code must match (e.g. 200)

	// Download state
	DownloadMinSize    int64    `yaml:"download_min_size,omitempty"`    // downloaded file must be at least N bytes
	DownloadMinRows    int      `yaml:"download_min_rows,omitempty"`   // CSV must have at least N data rows (excluding header)
	DownloadHasColumns []string `yaml:"download_has_columns,omitempty"` // CSV header must contain these columns

	// Metadata
	Label string `yaml:"label,omitempty"` // human-readable description for logging/errors
}

// Step is a single browser operation. Exactly one field should be set.
type Step struct {
	// Navigation
	Navigate string `yaml:"navigate,omitempty"`

	// Interactions
	Click    *ClickStep    `yaml:"click,omitempty"`
	Fill     *FillStep     `yaml:"fill,omitempty"`
	Select   *SelectStep   `yaml:"select,omitempty"`
	Upload   *UploadStep   `yaml:"upload,omitempty"`
	Download *DownloadStep `yaml:"download,omitempty"`

	// Waits
	WaitVisible *WaitStep    `yaml:"wait_visible,omitempty"`
	WaitText    *WaitStep    `yaml:"wait_text,omitempty"`
	WaitURL     *WaitURLStep `yaml:"wait_url,omitempty"`

	// Utilities
	Screenshot string     `yaml:"screenshot,omitempty"`
	Sleep      *SleepStep `yaml:"sleep,omitempty"`
	Eval       string     `yaml:"eval,omitempty"`

	// Control flow
	Label    string `yaml:"label,omitempty"`    // human-readable step description for logging
	Optional bool   `yaml:"optional,omitempty"` // if true, step failure is non-fatal
}

type ClickStep struct {
	Selector string `yaml:"selector"`
	Text     string `yaml:"text,omitempty"` // match by visible text
	Nth      int    `yaml:"nth,omitempty"`  // 0-indexed, use when multiple matches
	Timeout  string `yaml:"timeout,omitempty"`
}

type FillStep struct {
	Selector string `yaml:"selector"`
	Value    string `yaml:"value"`          // supports ${ENV_VAR} interpolation
	Clear    bool   `yaml:"clear,omitempty"` // clear field before filling
}

type SelectStep struct {
	Selector string `yaml:"selector"`
	Value    string `yaml:"value,omitempty"`   // option value to select (supports ${ENV_VAR})
	Text     string `yaml:"text,omitempty"`    // match by visible text instead of value
	Timeout  string `yaml:"timeout,omitempty"` // default 5s
}

type UploadStep struct {
	Selector string `yaml:"selector"`
	Source   string `yaml:"source"` // "result" to use previous step's output, or a path
}

type DownloadStep struct {
	Timeout  string `yaml:"timeout,omitempty"`
	SaveAs   string `yaml:"save_as,omitempty"` // optional filename pattern
}

type WaitStep struct {
	Selector string `yaml:"selector,omitempty"`
	Text     string `yaml:"text,omitempty"`
	State    string `yaml:"state,omitempty"` // visible, attached, hidden
	Timeout  string `yaml:"timeout,omitempty"`
}

type WaitURLStep struct {
	Match   string `yaml:"match"`
	Timeout string `yaml:"timeout,omitempty"`
}

type SleepStep struct {
	Duration string `yaml:"duration"`
}

// ParseTimeout converts a timeout string like "30s" or "2m" to a Duration.
// Defaults to 30s if empty or unparseable.
func ParseTimeout(s string) time.Duration {
	if s == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
