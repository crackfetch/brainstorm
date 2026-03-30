package workflow

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// EvalResult holds the outcome of running eval assertions on an action.
type EvalResult struct {
	Passed int
	Failed int
	Errors []string
}

// runEvals executes post-action assertions against the current page and
// download state. Returns nil if there are no eval assertions defined.
func (e *Executor) runEvals(name string, action Action) *EvalResult {
	if len(action.Eval) == 0 {
		return nil
	}

	result := &EvalResult{}

	for i, assert := range action.Eval {
		label := assert.Label
		if label == "" {
			label = fmt.Sprintf("eval %d", i+1)
		}

		if e.debug {
			log.Printf("[%s] eval: %s", name, label)
		}

		if err := e.runOneEval(assert); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", label, err))
			if e.debug {
				log.Printf("[%s] eval FAILED: %s: %v", name, label, err)
			}
		} else {
			result.Passed++
			if e.debug {
				log.Printf("[%s] eval PASSED: %s", name, label)
			}
		}
	}

	return result
}

// evalTimeout returns the timeout for a page-state assertion.
// Defaults to 5s if not specified.
func evalTimeout(assert EvalAssert) time.Duration {
	if assert.Timeout != "" {
		return ParseTimeout(assert.Timeout)
	}
	return 5 * time.Second
}

func (e *Executor) runOneEval(assert EvalAssert) error {
	switch {
	case assert.JS != "":
		return e.evalJS(assert)

	case assert.URLContains != "":
		return e.evalURLContains(assert.URLContains)

	case assert.TextVisible != "":
		return e.evalTextVisible(assert)

	case assert.NoText != "":
		return e.evalNoText(assert)

	case assert.Selector != "":
		return e.evalSelector(assert)

	case assert.DownloadMinSize > 0:
		return e.evalDownloadMinSize(assert.DownloadMinSize)

	case assert.DownloadMinRows > 0:
		return e.evalDownloadMinRows(assert.DownloadMinRows)

	case len(assert.DownloadHasColumns) > 0:
		return e.evalDownloadHasColumns(assert.DownloadHasColumns)

	default:
		return fmt.Errorf("empty eval assertion (no check specified)")
	}
}

// evalJS runs a JavaScript expression and checks that it returns truthy.
func (e *Executor) evalJS(assert EvalAssert) error {
	if e.page == nil {
		return fmt.Errorf("[unreachable] no page available")
	}
	timeout := evalTimeout(assert)
	res, err := e.page.Timeout(timeout).Eval(assert.JS)
	if err != nil {
		return fmt.Errorf("[error] js eval failed: %w", err)
	}
	if val := res.Value; val.Nil() || !val.Bool() {
		return fmt.Errorf("[failed] js returned falsy: %s", assert.JS)
	}
	return nil
}

// evalURLContains checks that the current page URL contains the given string.
func (e *Executor) evalURLContains(match string) error {
	if e.page == nil {
		return fmt.Errorf("[unreachable] no page available")
	}
	info, err := e.page.Info()
	if err != nil {
		return fmt.Errorf("[error] get page info: %w", err)
	}
	if !strings.Contains(info.URL, match) {
		return fmt.Errorf("[failed] URL %q does not contain %q", info.URL, match)
	}
	return nil
}

// evalTextVisible checks that the given text is visible somewhere on the page.
func (e *Executor) evalTextVisible(assert EvalAssert) error {
	if e.page == nil {
		return fmt.Errorf("[unreachable] no page available")
	}
	timeout := evalTimeout(assert)
	_, err := e.page.Timeout(timeout).ElementR("*", assert.TextVisible)
	if err != nil {
		return fmt.Errorf("[failed] text %q not found on page", assert.TextVisible)
	}
	return nil
}

// evalNoText checks that the given text is NOT visible on the page.
func (e *Executor) evalNoText(assert EvalAssert) error {
	if e.page == nil {
		return fmt.Errorf("[unreachable] no page available")
	}
	// Short timeout for absence checks — we're confirming something ISN'T there.
	timeout := 2 * time.Second
	if assert.Timeout != "" {
		timeout = ParseTimeout(assert.Timeout)
	}
	el, err := e.page.Timeout(timeout).ElementR("*", assert.NoText)
	if err == nil && el != nil {
		return fmt.Errorf("[failed] text %q should not be on page but was found", assert.NoText)
	}
	return nil
}

// evalSelector checks that an element matching the selector exists on the page.
func (e *Executor) evalSelector(assert EvalAssert) error {
	if e.page == nil {
		return fmt.Errorf("[unreachable] no page available")
	}
	timeout := evalTimeout(assert)
	_, err := e.page.Timeout(timeout).Element(assert.Selector)
	if err != nil {
		return fmt.Errorf("[failed] selector %q not found on page", assert.Selector)
	}
	return nil
}

// evalDownloadMinSize checks that the last downloaded file is at least N bytes.
func (e *Executor) evalDownloadMinSize(minBytes int64) error {
	if e.LastDownload == "" {
		return fmt.Errorf("[unreachable] no file was downloaded")
	}
	info, err := os.Stat(e.LastDownload)
	if err != nil {
		return fmt.Errorf("[error] stat downloaded file: %w", err)
	}
	if info.Size() < minBytes {
		return fmt.Errorf("[failed] downloaded file is %d bytes, need at least %d", info.Size(), minBytes)
	}
	return nil
}

// evalDownloadMinRows checks that the last downloaded CSV has at least N data
// rows (excluding the header row).
func (e *Executor) evalDownloadMinRows(minRows int) error {
	if e.LastDownload == "" {
		return fmt.Errorf("[unreachable] no file was downloaded")
	}
	f, err := os.Open(e.LastDownload)
	if err != nil {
		return fmt.Errorf("[error] open downloaded file: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	// Skip header
	if _, err := reader.Read(); err != nil {
		return fmt.Errorf("[error] read CSV header: %w", err)
	}

	count := 0
	for {
		_, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("[error] read CSV row %d: %w", count+1, err)
		}
		count++
		// Short-circuit: once we've seen enough rows, no need to read the whole file.
		if count >= minRows {
			return nil
		}
	}

	return fmt.Errorf("[failed] CSV has %d data rows, need at least %d", count, minRows)
}

// evalDownloadHasColumns checks that the last downloaded CSV file contains
// all the specified column names in its header row.
func (e *Executor) evalDownloadHasColumns(columns []string) error {
	if e.LastDownload == "" {
		return fmt.Errorf("[unreachable] no file was downloaded")
	}
	f, err := os.Open(e.LastDownload)
	if err != nil {
		return fmt.Errorf("[error] open downloaded file: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("[error] read CSV header: %w", err)
	}

	headerSet := make(map[string]bool, len(header))
	for _, col := range header {
		headerSet[strings.TrimSpace(col)] = true
	}

	var missing []string
	for _, want := range columns {
		if !headerSet[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("[failed] CSV missing columns: %s (has: %s)", strings.Join(missing, ", "), strings.Join(header, ", "))
	}
	return nil
}
