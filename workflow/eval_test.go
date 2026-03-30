package workflow

import (
	"os"
	"path/filepath"
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

func TestEvalTextVisible_NoPage(t *testing.T) {
	exec := &Executor{}
	if err := exec.evalTextVisible(EvalAssert{TextVisible: "hello"}); err == nil {
		t.Error("expected fail with no page")
	}
}

func TestEvalNoText_NoPage(t *testing.T) {
	exec := &Executor{}
	if err := exec.evalNoText(EvalAssert{NoText: "error"}); err == nil {
		t.Error("expected fail with no page")
	}
}

func TestEvalSelector_NoPage(t *testing.T) {
	exec := &Executor{}
	if err := exec.evalSelector(EvalAssert{Selector: "#btn"}); err == nil {
		t.Error("expected fail with no page")
	}
}

func TestEvalURLContains_NoPage(t *testing.T) {
	exec := &Executor{}
	if err := exec.evalURLContains("example"); err == nil {
		t.Error("expected fail with no page")
	}
}

func TestEvalJS_NoPage(t *testing.T) {
	exec := &Executor{}
	if err := exec.evalJS(EvalAssert{JS: "true"}); err == nil {
		t.Error("expected fail with no page")
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
