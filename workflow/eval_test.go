package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvalAssertParsing(t *testing.T) {
	yaml := `
name: test-eval
actions:
  export:
    url: https://example.com/export
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }
    eval:
      - label: "Check file size"
        download_min_size: 100
      - label: "Check CSV columns"
        download_has_columns: ["Product Name", "Price"]
      - label: "Check row count"
        download_min_rows: 50
      - js: "!document.querySelector('.error')"
      - url_contains: "export"
      - text_visible: "Export complete"
      - no_text: "Error"
      - selector: "#success-banner"
`
	w, err := LoadFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	action := w.Actions["export"]
	if len(action.Eval) != 8 {
		t.Fatalf("expected 8 eval assertions, got %d", len(action.Eval))
	}

	// Check each assertion parsed correctly
	if action.Eval[0].DownloadMinSize != 100 {
		t.Errorf("eval 0: expected download_min_size=100, got %d", action.Eval[0].DownloadMinSize)
	}
	if action.Eval[0].Label != "Check file size" {
		t.Errorf("eval 0: expected label 'Check file size', got %q", action.Eval[0].Label)
	}
	if len(action.Eval[1].DownloadHasColumns) != 2 {
		t.Errorf("eval 1: expected 2 columns, got %d", len(action.Eval[1].DownloadHasColumns))
	}
	if action.Eval[2].DownloadMinRows != 50 {
		t.Errorf("eval 2: expected download_min_rows=50, got %d", action.Eval[2].DownloadMinRows)
	}
	if action.Eval[3].JS != "!document.querySelector('.error')" {
		t.Errorf("eval 3: wrong JS expression")
	}
	if action.Eval[4].URLContains != "export" {
		t.Errorf("eval 4: wrong url_contains")
	}
	if action.Eval[5].TextVisible != "Export complete" {
		t.Errorf("eval 5: wrong text_visible")
	}
	if action.Eval[6].NoText != "Error" {
		t.Errorf("eval 6: wrong no_text")
	}
	if action.Eval[7].Selector != "#success-banner" {
		t.Errorf("eval 7: wrong selector")
	}
}

func TestEvalDownloadMinSize(t *testing.T) {
	// Create a temp file with known content
	dir := t.TempDir()
	path := filepath.Join(dir, "test.csv")
	os.WriteFile(path, []byte("header1,header2\nval1,val2\n"), 0644)

	exec := &Executor{LastDownload: path}

	// Should pass: file is 26 bytes
	if err := exec.evalDownloadMinSize(10); err != nil {
		t.Errorf("expected pass for min_size=10, got: %v", err)
	}

	// Should fail: file is only 26 bytes
	if err := exec.evalDownloadMinSize(1000); err == nil {
		t.Error("expected fail for min_size=1000")
	}

	// Should fail: no download
	exec2 := &Executor{}
	if err := exec2.evalDownloadMinSize(1); err == nil {
		t.Error("expected fail with no download")
	}
}

func TestEvalDownloadMinRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.csv")
	os.WriteFile(path, []byte("Name,Price\nWidget,9.99\nGadget,4.50\nThing,1.00\n"), 0644)

	exec := &Executor{LastDownload: path}

	// Should pass: 3 data rows >= 2
	if err := exec.evalDownloadMinRows(2); err != nil {
		t.Errorf("expected pass for min_rows=2, got: %v", err)
	}

	// Should pass: 3 data rows >= 3
	if err := exec.evalDownloadMinRows(3); err != nil {
		t.Errorf("expected pass for min_rows=3, got: %v", err)
	}

	// Should fail: 3 data rows < 10
	if err := exec.evalDownloadMinRows(10); err == nil {
		t.Error("expected fail for min_rows=10")
	}

	// Should fail: no download
	exec2 := &Executor{}
	if err := exec2.evalDownloadMinRows(1); err == nil {
		t.Error("expected fail with no download")
	}
}

func TestEvalDownloadMinRows_BOM(t *testing.T) {
	// BOM CSV should still count rows correctly.
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.csv")
	os.WriteFile(path, []byte("\xef\xbb\xbfName,Price\nWidget,9.99\n"), 0644)

	exec := &Executor{LastDownload: path}
	if err := exec.evalDownloadMinRows(1); err != nil {
		t.Errorf("BOM CSV: expected pass for min_rows=1, got: %v", err)
	}
}

func TestEvalDownloadMinRows_HeaderOnly(t *testing.T) {
	// A CSV with a header but zero data rows should fail min_rows=1.
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.csv")
	os.WriteFile(path, []byte("Name,Price\n"), 0644)

	exec := &Executor{LastDownload: path}

	if err := exec.evalDownloadMinRows(1); err == nil {
		t.Error("expected fail for header-only CSV with min_rows=1")
	}

	// min_rows=0 would be meaningless but shouldn't panic
	if err := exec.evalDownloadMinRows(0); err != nil {
		t.Errorf("expected pass for min_rows=0, got: %v", err)
	}
}

func TestEvalDownloadHasColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.csv")
	os.WriteFile(path, []byte("Product Name,Price,Quantity\nWidget,9.99,5\n"), 0644)

	exec := &Executor{LastDownload: path}

	// Should pass: all columns present
	if err := exec.evalDownloadHasColumns([]string{"Product Name", "Price"}); err != nil {
		t.Errorf("expected pass, got: %v", err)
	}

	// Should fail: missing column
	if err := exec.evalDownloadHasColumns([]string{"Product Name", "SKU"}); err == nil {
		t.Error("expected fail for missing column 'SKU'")
	}

	// Should fail: no download
	exec2 := &Executor{}
	if err := exec2.evalDownloadHasColumns([]string{"Name"}); err == nil {
		t.Error("expected fail with no download")
	}
}

func TestEvalDownloadHasColumns_BOM(t *testing.T) {
	// Windows Excel prepends a UTF-8 BOM (\xef\xbb\xbf) to CSV files.
	// evalDownloadHasColumns should still match the first column name.
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.csv")
	os.WriteFile(path, []byte("\xef\xbb\xbfProduct Name,Price,Quantity\nWidget,9.99,5\n"), 0644)

	exec := &Executor{LastDownload: path}

	if err := exec.evalDownloadHasColumns([]string{"Product Name", "Price"}); err != nil {
		t.Errorf("BOM CSV: expected pass, got: %v", err)
	}
}

func TestEvalStatusCode(t *testing.T) {
	exec := &Executor{LastStatusCode: 200}

	// Should pass: 200 matches 200
	if err := exec.runOneEval(EvalAssert{StatusCode: 200}); err != nil {
		t.Errorf("expected pass for status 200, got: %v", err)
	}

	// Should fail: got 200, want 500
	if err := exec.runOneEval(EvalAssert{StatusCode: 500}); err == nil {
		t.Error("expected fail for status_code=500 when actual is 200")
	}

	// Should pass: 404 matches 404
	exec.LastStatusCode = 404
	if err := exec.runOneEval(EvalAssert{StatusCode: 404}); err != nil {
		t.Errorf("expected pass for status 404, got: %v", err)
	}
}

func TestEvalStatusCode_NotCaptured(t *testing.T) {
	// When no navigation happened (status code is 0), the eval should error
	exec := &Executor{}
	if err := exec.runOneEval(EvalAssert{StatusCode: 200}); err == nil {
		t.Error("expected error when status code not captured")
	}
}

func TestPageEvals_NoPage(t *testing.T) {
	// All page-state evals should return an [unreachable] error when no browser
	// is connected. This guards against nil-pointer panics in production when
	// an eval runs after a browser crash or failed launch.
	exec := &Executor{}

	cases := []struct {
		name   string
		assert EvalAssert
	}{
		{"js", EvalAssert{JS: "true"}},
		{"url_contains", EvalAssert{URLContains: "example"}},
		{"text_visible", EvalAssert{TextVisible: "hello"}},
		{"no_text", EvalAssert{NoText: "error"}},
		{"selector", EvalAssert{Selector: "#btn"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := exec.runOneEval(tc.assert)
			if err == nil {
				t.Errorf("%s: expected error with no page, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "[unreachable]") {
				t.Errorf("%s: expected [unreachable] prefix, got: %v", tc.name, err)
			}
		})
	}
}

func TestRunEvals_NoAssertions(t *testing.T) {
	exec := &Executor{}
	action := Action{Steps: []Step{}}
	result := exec.runEvals("test", action)
	if result != nil {
		t.Error("expected nil result when no eval assertions defined")
	}
}

func TestRunEvals_MixedResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.csv")
	os.WriteFile(path, []byte("Name,Price\nWidget,9.99\n"), 0644)

	exec := &Executor{LastDownload: path}
	action := Action{
		Eval: []EvalAssert{
			{Label: "size check", DownloadMinSize: 10},       // pass
			{Label: "column check", DownloadHasColumns: []string{"Name", "Missing"}}, // fail
			{Label: "size check 2", DownloadMinSize: 5},      // pass
		},
	}

	result := exec.runEvals("test", action)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Passed != 2 {
		t.Errorf("expected 2 passed, got %d", result.Passed)
	}
	if result.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", result.Failed)
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestEvalEmptyAssert(t *testing.T) {
	exec := &Executor{}
	err := exec.runOneEval(EvalAssert{})
	if err == nil {
		t.Error("expected error for empty assertion")
	}
}
