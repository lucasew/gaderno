package session

import (
	"context"
	"testing"

	"github.com/lucasew/gaderno/internal/document"
	"github.com/lucasew/gaderno/internal/kernel"
	"github.com/lucasew/gaderno/internal/store"
)

func TestOpenSave(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("x = 1")
	// no resolvable kernelspec → NeedsKernel
	if err := st.Save(context.Background(), "n.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	h, err := Open(context.Background(), st, dir, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	if h.SessionID == "" {
		t.Fatal("expected non-empty SessionID")
	}
	stt := h.Status()
	if !stt.NeedsPick {
		t.Fatalf("expected needs kernel, got %+v", stt)
	}
	ids := h.Doc.CellIDs()
	if len(ids) != 1 {
		t.Fatal(ids)
	}
	if err := h.Doc.SetSourceServer(ids[0], "x = 2"); err != nil {
		t.Fatal(err)
	}
	if err := h.Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(context.Background(), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cells[0].SourceString() != "x = 2" {
		t.Fatalf("%q", got.Cells[0].SourceString())
	}
}

func TestBindKernelUnknown(t *testing.T) {
	kernel.ResetCatalogForTest()
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	_ = st.Save(context.Background(), "n.ipynb", nb)
	h, err := Open(context.Background(), st, dir, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	if err := h.BindKernel("definitely-missing-kernel-xyz"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSessionIDDistinctPerOpen(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	if err := st.Save(context.Background(), "n.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	h1, err := Open(context.Background(), st, dir, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	defer h1.Close(context.Background())
	h2, err := Open(context.Background(), st, dir, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close(context.Background())
	if h1.SessionID == "" || h2.SessionID == "" {
		t.Fatal("empty session id")
	}
	if h1.SessionID == h2.SessionID {
		t.Fatalf("expected distinct session ids, both %q", h1.SessionID)
	}
}

func TestOutputsFromExecute(t *testing.T) {
	res := kernel.ExecuteResult{
		Status:         "ok",
		ExecutionCount: 2,
		Stdout:         "hi\n",
	}
	displays := []kernel.DisplayData{
		{
			OutputType: "execute_result",
			Data:       map[string]any{"text/plain": "1"},
		},
	}
	outs := outputsFromExecute(res, displays)
	if len(outs) != 2 {
		t.Fatalf("len=%d %#v", len(outs), outs)
	}
	if outs[0].OutputType != "stream" || outs[0].Name != "stdout" {
		t.Fatalf("out0 %#v", outs[0])
	}
	if outs[1].OutputType != "execute_result" {
		t.Fatalf("out1 %#v", outs[1])
	}
	if outs[1].ExecutionCount == nil || *outs[1].ExecutionCount != 2 {
		t.Fatalf("execute_result count %#v", outs[1].ExecutionCount)
	}

	errRes := kernel.ExecuteResult{Status: "error", Ename: "E", Evalue: "v", ExecutionCount: 3}
	eouts := outputsFromExecute(errRes, nil)
	if len(eouts) != 1 || eouts[0].OutputType != "error" {
		t.Fatalf("error outs %#v", eouts)
	}
}

func TestExecuteCellRecordsOutputsInCRDT(t *testing.T) {
	// Without a live kernel ExecuteCell returns early — exercise clear/apply
	// helpers via Doc the same way ExecuteCell does after a run.
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	nb.Cells[0].Source = document.NewMultiline("print(1)")
	if err := st.Save(context.Background(), "n.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	h, err := Open(context.Background(), st, dir, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	id := h.Doc.CellIDs()[0]

	if err := h.Doc.ClearCellOutputs(id); err != nil {
		t.Fatal(err)
	}
	res := kernel.ExecuteResult{Status: "ok", ExecutionCount: 1, Stdout: "1\n"}
	if err := h.Doc.ApplyCellExecution(id, outputsFromExecute(res, nil), execCountPtr(res), cellStatusFromExecute(res)); err != nil {
		t.Fatal(err)
	}
	if err := h.Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load(context.Background(), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cells[0].ExecutionCount == nil || *got.Cells[0].ExecutionCount != 1 {
		t.Fatalf("saved execution_count=%v", got.Cells[0].ExecutionCount)
	}
	if len(got.Cells[0].Outputs) != 1 || got.Cells[0].Outputs[0].OutputType != "stream" {
		t.Fatalf("saved outputs %#v", got.Cells[0].Outputs)
	}
	if txt := got.Cells[0].Outputs[0].Text.String(); txt != "1\n" {
		t.Fatalf("stream %q", txt)
	}
}

func TestOutputsFromExecuteIncludesTraceback(t *testing.T) {
	res := kernel.ExecuteResult{
		Status:    "error",
		Ename:     "ValueError",
		Evalue:    "boom",
		Traceback: []string{"Traceback (most recent call last):", "ValueError: boom"},
	}
	outs := outputsFromExecute(res, nil)
	var errOut *document.Output
	for i := range outs {
		if outs[i].OutputType == "error" {
			errOut = &outs[i]
			break
		}
	}
	if errOut == nil {
		t.Fatalf("no error output: %#v", outs)
	}
	if errOut.Ename != "ValueError" || errOut.Evalue != "boom" {
		t.Fatalf("%#v", errOut)
	}
	if len(errOut.Traceback) != 2 || errOut.Traceback[1] != "ValueError: boom" {
		t.Fatalf("traceback %#v", errOut.Traceback)
	}
}

func TestClientNotReadyBlocksSync(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	nb := document.NewEmpty()
	if err := st.Save(context.Background(), "n.ipynb", nb); err != nil {
		t.Fatal(err)
	}
	h, err := Open(context.Background(), st, dir, "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	c := h.AddClient("c1")
	if c.Ready {
		t.Fatal("new client should not be ready")
	}
	if h.ClientReady("c1") {
		t.Fatal("expected not ready")
	}
	if _, err := h.HandleSyncMessage("c1", []byte{0, 1, 2}); err == nil {
		t.Fatal("expected sync rejected before hello.ack")
	}
	if !h.MarkClientReady("c1") {
		t.Fatal("mark ready")
	}
	if !h.ClientReady("c1") {
		t.Fatal("expected ready")
	}
}
