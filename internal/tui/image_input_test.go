package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/luneCore"
)

type fakeClipboardImageReader struct {
	payload clipboardImagePayload
	err     error
}

func (reader fakeClipboardImageReader) Read(context.Context) (clipboardImagePayload, error) {
	return reader.payload, reader.err
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestImageAttachmentFromBytesValidatesAndDescribesPNG(t *testing.T) {
	data := testPNG(t, 16, 9)
	attachment, err := imageAttachmentFromBytes(data, "clipboard image", core.AttachmentLimits{MaxFileBytes: 1024})
	if err != nil {
		t.Fatalf("imageAttachmentFromBytes: %v", err)
	}
	if attachment.MediaType != "image/png" || attachment.Format != "PNG" || attachment.Width != 16 || attachment.Height != 9 || attachment.Size != int64(len(data)) {
		t.Fatalf("image metadata = %#v", attachment)
	}
	if _, err := imageAttachmentFromBytes(data, "large.png", core.AttachmentLimits{MaxFileBytes: 1}); err == nil {
		t.Fatal("oversized image was accepted")
	}
	if _, err := imageAttachmentFromBytes([]byte("not an image"), "bad.png", core.AttachmentLimits{}); err == nil {
		t.Fatal("invalid image data was accepted")
	}
}

func TestImageAttachmentsFromPathsRespectsFileAndTotalLimits(t *testing.T) {
	data := testPNG(t, 4, 3)
	first := filepath.Join(t.TempDir(), "first.png")
	second := filepath.Join(t.TempDir(), "second.PNG")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	attachments, skipped, err := imageAttachmentsFromPaths([]string{first, second, filepath.Join(t.TempDir(), "note.txt")},
		core.AttachmentLimits{MaxFiles: 5, MaxFileBytes: 1024, MaxTotalBytes: 1024})
	if err != nil {
		t.Fatalf("imageAttachmentsFromPaths: %v", err)
	}
	if len(attachments) != 2 || skipped != 1 || attachments[0].Filename != "first.png" {
		t.Fatalf("attachments/skipped = %#v/%d", attachments, skipped)
	}
	if _, _, err := imageAttachmentsFromPaths([]string{first, second}, core.AttachmentLimits{MaxFiles: 5, MaxFileBytes: 1024, MaxTotalBytes: int64(len(data))}); err == nil {
		t.Fatal("attachments over the total limit were accepted")
	}
	if _, _, err := imageAttachmentsFromPaths([]string{first}, core.AttachmentLimits{MaxFiles: 0, MaxFileBytes: 1, MaxTotalBytes: 1024}); err == nil {
		t.Fatal("attachment over the per-file limit was accepted")
	}
}

func TestImageAttachmentSetAppliesSafeDefaultsAndLimits(t *testing.T) {
	defaults := defaultAttachmentLimits()
	if defaults != (core.AttachmentLimits{MaxFiles: 5, MaxFileBytes: 10 << 20, MaxTotalBytes: 20 << 20}) {
		t.Fatalf("defaults = %#v", defaults)
	}
	large := imageAttachment{Filename: "large.png", Size: defaults.MaxFileBytes + 1}
	if err := validateImageAttachmentSet([]imageAttachment{large}, defaults); err == nil {
		t.Fatal("attachment set over the per-file default was accepted")
	}
}

func TestParsePastedImagePaths(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	paths, ok := parsePastedImagePaths("\"screenshots\u005c\u005cfirst image.PNG\"\n../outside/second.webp", workspace)
	if !ok || len(paths) != 2 {
		t.Fatalf("parsePastedImagePaths = %#v, %v", paths, ok)
	}
	if paths[0] != filepath.Join(workspace, "screenshots", "first image.PNG") || paths[1] != filepath.Clean(filepath.Join(workspace, "..", "outside", "second.webp")) {
		t.Fatalf("resolved paths = %#v", paths)
	}
	if _, ok := parsePastedImagePaths("Please inspect screenshot.png", workspace); ok {
		t.Fatal("prose ending in an image filename was classified as a path paste")
	}
	if _, ok := parsePastedImagePaths("image.png\nordinary text", workspace); ok {
		t.Fatal("mixed prose and paths were classified as an attachment-only paste")
	}
	quoted, ok := splitPastedPathLine(`"C:\Users\L\My Screenshots\one.png" "C:\Users\L\two.webp"`)
	if !ok || len(quoted) != 2 || quoted[0] != `C:\Users\L\My Screenshots\one.png` {
		t.Fatalf("quoted Windows paths = %#v, %v", quoted, ok)
	}
	apostrophe, ok := splitPastedPathLine(`C:\Users\L\D'Arcy\image.png`)
	if !ok || len(apostrophe) != 1 || apostrophe[0] != `C:\Users\L\D'Arcy\image.png` {
		t.Fatalf("path with apostrophe = %#v, %v", apostrophe, ok)
	}
}

func TestBuildTaskInputWithImagesPreservesTextOrderAndPortableData(t *testing.T) {
	data := testPNG(t, 2, 3)
	attachment := imageAttachment{
		Token:     "[Image #1 · PNG · 2×3 · " + formatAttachmentBytes(int64(len(data))) + "]",
		MediaType: "image/png", Format: "PNG", Width: 2, Height: 3, Size: int64(len(data)), Data: data,
	}
	input, err := buildTaskInputWithImages("before "+attachment.Token+" after", []imageAttachment{attachment})
	if err != nil {
		t.Fatalf("buildTaskInputWithImages: %v", err)
	}
	var items []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL string `json:"image_url"`
		} `json:"content"`
	}
	if err := json.Unmarshal(input.Raw, &items); err != nil {
		t.Fatalf("decode task input: %v", err)
	}
	if len(items) != 1 || items[0].Type != "message" || items[0].Role != "user" || len(items[0].Content) != 4 {
		t.Fatalf("structured input = %#v", items)
	}
	if items[0].Content[0].Text != "before " || items[0].Content[1].Text != attachment.Token || items[0].Content[2].Type != "input_image" || items[0].Content[3].Text != " after" {
		t.Fatalf("content order = %#v", items[0].Content)
	}
	if !strings.HasPrefix(items[0].Content[2].ImageURL, "data:image/png;base64,") {
		t.Fatalf("image URL = %q", items[0].Content[2].ImageURL)
	}
	encoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(items[0].Content[2].ImageURL, "data:image/png;base64,"))
	if err != nil || !bytes.Equal(encoded, data) {
		t.Fatalf("decoded image data mismatch, err=%v", err)
	}
}

func TestTUIClipboardImagePasteSubmitsStructuredImageInPlanMode(t *testing.T) {
	data := testPNG(t, 8, 5)
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.clipboardReader = fakeClipboardImageReader{payload: clipboardImagePayload{Image: data}}
	ui.clipboardTextReader = func() (string, error) { return "", nil }
	ui.planMode = true

	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if command == nil {
		t.Fatal("Ctrl+V did not start an asynchronous clipboard read")
	}
	updated, _ = updated.Update(command())
	ui = updated.(*tuiModel)
	if len(ui.attachments) != 1 || !strings.Contains(ui.input.Value(), ui.attachments[0].Token) {
		t.Fatalf("clipboard image draft = %q, attachments=%#v", ui.input.Value(), ui.attachments)
	}
	token := ui.attachments[0].Token

	updated, command = ui.submit()
	if command == nil {
		t.Fatal("image-only plan message did not start a task")
	}
	updated, _ = updated.Update(command())
	ui = updated.(*tuiModel)
	if len(client.inputs) != 1 || !client.inputs[0].PlanMode {
		t.Fatalf("submitted requests = %#v, want one Plan mode request", client.inputs)
	}
	input := string(client.inputs[0].Input.Raw)
	if !strings.Contains(input, `"type":"input_image"`) || !strings.Contains(input, base64.StdEncoding.EncodeToString(data)) {
		t.Fatalf("submitted input did not contain the clipboard image: %s", input)
	}
	transcript := inputTranscriptText(client.inputs[0].Input, "")
	if !strings.Contains(transcript, token) || strings.Contains(transcript, "[image]") {
		t.Fatalf("image transcript = %q, want one editable metadata tag and no duplicate marker", transcript)
	}
}

func TestTUICtrlVPastesTextWhenClipboardHasNoImage(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.clipboardReader = fakeClipboardImageReader{}
	ui.clipboardTextReader = func() (string, error) { return "ordinary clipboard text", nil }
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if command == nil {
		t.Fatal("Ctrl+V did not start a clipboard read")
	}
	updated, _ = updated.Update(command())
	ui = updated.(*tuiModel)
	if ui.input.Value() != "ordinary clipboard text" || len(ui.attachments) != 0 {
		t.Fatalf("text clipboard paste = %q with attachments %#v", ui.input.Value(), ui.attachments)
	}
}

func TestTUIClipboardUsesImageFileListBeforeBitmap(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "clipboard.png")
	if err := os.WriteFile(path, testPNG(t, 5, 4), 0600); err != nil {
		t.Fatal(err)
	}
	ui := newTUIModel(&fakeResponder{}, false)
	ui.workspace = workspace
	ui.clipboardReader = fakeClipboardImageReader{payload: clipboardImagePayload{
		Files: []string{path}, Image: testPNG(t, 99, 99),
	}}
	ui.clipboardTextReader = func() (string, error) { return "", nil }
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if command == nil {
		t.Fatal("Ctrl+V did not start a clipboard read")
	}
	updated, _ = updated.Update(command())
	ui = updated.(*tuiModel)
	if len(ui.attachments) != 1 || ui.attachments[0].Filename != "clipboard.png" || ui.attachments[0].Width != 5 {
		t.Fatalf("clipboard file list did not take precedence: %#v", ui.attachments)
	}
}

func TestTUIClipboardImageRespectsRemainingDraftLimit(t *testing.T) {
	data := testPNG(t, 2, 2)
	first, err := imageAttachmentFromBytes(data, "first.png", core.AttachmentLimits{})
	if err != nil {
		t.Fatal(err)
	}
	first.Token = imageAttachmentToken(1, first)
	ui := newTUIModel(&fakeResponder{}, false)
	ui.attachmentLimits = core.AttachmentLimits{MaxFiles: 1, MaxFileBytes: 1024, MaxTotalBytes: 2048}
	ui.attachments = []imageAttachment{first}
	ui.input.SetValue(first.Token)
	ui.clipboardReader = fakeClipboardImageReader{payload: clipboardImagePayload{Image: data}}
	ui.clipboardTextReader = func() (string, error) { return "", nil }
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if command == nil {
		t.Fatal("Ctrl+V did not start a clipboard read")
	}
	updated, _ = updated.Update(command())
	ui = updated.(*tuiModel)
	if len(ui.attachments) != 1 || !strings.Contains(ui.status, "attachments.maxFiles allows 1") {
		t.Fatalf("limit violation changed draft: attachments=%d status=%q", len(ui.attachments), ui.status)
	}
}

func TestTUIPastedImagePathsAttachAndInvalidPathsRemainText(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "screen.png")
	if err := os.WriteFile(path, testPNG(t, 12, 7), 0600); err != nil {
		t.Fatal(err)
	}
	ui := newTUIModel(&fakeResponder{}, false)
	ui.workspace = workspace
	updated, command := ui.Update(tea.PasteMsg{Content: path})
	if command == nil {
		t.Fatal("image path paste did not start a file read")
	}
	updated, _ = updated.Update(command())
	ui = updated.(*tuiModel)
	if len(ui.attachments) != 1 || !strings.Contains(ui.input.Value(), "[Image #1 · PNG · 12×7 · ") {
		t.Fatalf("path paste draft = %q, attachments=%#v", ui.input.Value(), ui.attachments)
	}

	missing := filepath.Join(workspace, "missing.png")
	updated, command = ui.Update(tea.PasteMsg{Content: missing})
	if command == nil {
		t.Fatal("missing image path paste did not start a file read")
	}
	updated, _ = updated.Update(command())
	ui = updated.(*tuiModel)
	if !strings.Contains(ui.input.Value(), missing) || len(ui.attachments) != 1 || !strings.Contains(ui.status, "pasted content remains") {
		t.Fatalf("failed path paste did not preserve source text: draft=%q status=%q attachments=%d", ui.input.Value(), ui.status, len(ui.attachments))
	}

	prose := "Please inspect screen.png"
	updated, command = ui.Update(tea.PasteMsg{Content: prose})
	if command != nil || !strings.Contains(ui.input.Value(), prose) {
		t.Fatalf("prose paste was treated as a file path: command=%v draft=%q", command != nil, ui.input.Value())
	}
}

func TestTUIImagePlaceholderDeletionUndoAndRedoRestoreAttachment(t *testing.T) {
	data := testPNG(t, 3, 2)
	attachment, err := imageAttachmentFromBytes(data, "small.png", core.AttachmentLimits{})
	if err != nil {
		t.Fatal(err)
	}
	attachment.Token = imageAttachmentToken(1, attachment)
	ui := newTUIModel(&fakeResponder{}, false)
	ui.input.SetValue(attachment.Token)
	ui.attachments = []imageAttachment{attachment}
	ui.input.SelectAll()

	originalWriter := writeClipboard
	t.Cleanup(func() { writeClipboard = originalWriter })
	writeClipboard = func(string) error { return nil }
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	if ui.input.Value() != "" || len(ui.attachments) != 0 {
		t.Fatalf("deleting image token left draft=%q attachments=%d", ui.input.Value(), len(ui.attachments))
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModCtrl}))
	if ui.input.Value() != attachment.Token || len(ui.attachments) != 1 {
		t.Fatalf("undo did not restore image: draft=%q attachments=%d", ui.input.Value(), len(ui.attachments))
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Mod: tea.ModCtrl}))
	if ui.input.Value() != "" || len(ui.attachments) != 0 {
		t.Fatalf("redo did not remove image: draft=%q attachments=%d", ui.input.Value(), len(ui.attachments))
	}
}

func TestTUIQueuedInputKeepsImagesWithoutRetainingEncodedRequest(t *testing.T) {
	data := testPNG(t, 4, 2)
	attachment, err := imageAttachmentFromBytes(data, "queued.png", core.AttachmentLimits{})
	if err != nil {
		t.Fatal(err)
	}
	attachment.Token = imageAttachmentToken(1, attachment)
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.planMode = true
	ui.attachments = []imageAttachment{attachment}
	ui.input.SetValue("Review this " + attachment.Token)
	ui.enqueueInput()
	if len(ui.queued) != 1 || len(ui.queued[0].attachments) != 1 {
		t.Fatalf("queued message does not hold raw attachment separately: %#v", ui.queued)
	}
	if ui.queued[0].planMode != true {
		t.Fatal("queued image did not preserve Plan mode")
	}

	updated, command := ui.startQueuedInput()
	if command == nil {
		t.Fatal("queued image did not start")
	}
	updated, _ = updated.Update(command())
	if len(client.inputs) != 1 || !client.inputs[0].PlanMode || !strings.Contains(string(client.inputs[0].Input.Raw), `"type":"input_image"`) {
		t.Fatalf("queued image request = %#v", client.inputs)
	}
}

func TestTUISlashCommandDoesNotDiscardImageDraft(t *testing.T) {
	data := testPNG(t, 2, 2)
	attachment, err := imageAttachmentFromBytes(data, "draft.png", core.AttachmentLimits{})
	if err != nil {
		t.Fatal(err)
	}
	attachment.Token = imageAttachmentToken(1, attachment)
	ui := newTUIModel(&fakeResponder{}, false)
	ui.attachments = []imageAttachment{attachment}
	ui.input.SetValue("/help")
	ui.submit()
	if len(ui.attachments) != 1 || ui.input.Value() != "/help" {
		t.Fatalf("slash command discarded image draft: input=%q attachments=%d", ui.input.Value(), len(ui.attachments))
	}
}
