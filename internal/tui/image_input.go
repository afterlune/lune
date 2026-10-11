package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/afterlune/luneCore"
	"github.com/atotto/clipboard"
	clip "golang.design/x/clipboard"
	_ "golang.org/x/image/webp"
)

type imageAttachment struct {
	Token     string
	Filename  string
	MediaType string
	Format    string
	Width     int
	Height    int
	Size      int64
	Data      []byte
}

type clipboardImagePayload struct {
	Files []string
	Image []byte
}

type clipboardImageReader interface {
	Read(context.Context) (clipboardImagePayload, error)
}

type systemClipboardImageReader struct{}

var readClipboardText = clipboard.ReadAll

type imageInputReadyMsg struct {
	Attachments  []imageAttachment
	FallbackText string
	Skipped      int
	Err          error
}

var clipboardInitOnce sync.Once
var clipboardInitErr error

func (systemClipboardImageReader) Read(ctx context.Context) (clipboardImagePayload, error) {
	clipboardInitOnce.Do(func() { clipboardInitErr = clip.Init() })
	if clipboardInitErr != nil {
		return clipboardImagePayload{}, clipboardInitErr
	}

	files, filesErr := clip.ReadFiles(ctx)
	if filesErr == nil && len(files) > 0 {
		return clipboardImagePayload{Files: files}, nil
	}
	if filesErr != nil && !errors.Is(filesErr, clip.ErrNoData) {
		return clipboardImagePayload{}, filesErr
	}

	imageBytes, err := clip.Read(ctx, clip.FmtImage)
	if errors.Is(err, clip.ErrNoData) {
		return clipboardImagePayload{}, nil
	}
	if err != nil {
		return clipboardImagePayload{}, err
	}
	if len(imageBytes) == 0 {
		return clipboardImagePayload{}, nil
	}
	return clipboardImagePayload{Image: imageBytes}, nil
}

func defaultAttachmentLimits() core.AttachmentLimits {
	return core.AttachmentLimits{
		MaxFiles: core.DefaultAttachmentMaxFiles, MaxFileBytes: core.DefaultAttachmentMaxFileBytes,
		MaxTotalBytes: core.DefaultAttachmentMaxTotalBytes,
	}
}

func normalizeAttachmentLimits(limits core.AttachmentLimits) core.AttachmentLimits {
	defaults := defaultAttachmentLimits()
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaults.MaxFiles
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = defaults.MaxTotalBytes
	}
	return limits
}

func validateImageAttachmentSet(attachments []imageAttachment, limits core.AttachmentLimits) error {
	limits = normalizeAttachmentLimits(limits)
	if len(attachments) > limits.MaxFiles {
		return fmt.Errorf("too many images; attachments.maxFiles allows %d", limits.MaxFiles)
	}
	totalBytes := int64(0)
	for _, attachment := range attachments {
		if attachment.Size > limits.MaxFileBytes {
			return fmt.Errorf("%s is %s; attachments.maxFileBytes allows %s", attachment.Filename,
				formatAttachmentBytes(attachment.Size), formatAttachmentBytes(limits.MaxFileBytes))
		}
		if attachment.Size > limits.MaxTotalBytes || totalBytes > limits.MaxTotalBytes-attachment.Size {
			return fmt.Errorf("images exceed attachments.maxTotalBytes (%s allowed)", formatAttachmentBytes(limits.MaxTotalBytes))
		}
		totalBytes += attachment.Size
	}
	return nil
}

func imageAttachmentFromBytes(data []byte, filename string, limits core.AttachmentLimits) (imageAttachment, error) {
	if len(data) == 0 {
		return imageAttachment{}, errors.New("image data is empty")
	}
	limits = normalizeAttachmentLimits(limits)
	if limits.MaxFileBytes > 0 && int64(len(data)) > limits.MaxFileBytes {
		return imageAttachment{}, fmt.Errorf("image is %s; per-image limit is %s", formatAttachmentBytes(int64(len(data))), formatAttachmentBytes(limits.MaxFileBytes))
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return imageAttachment{}, fmt.Errorf("could not decode image: %w", err)
	}
	mediaType, label, ok := imageFormat(format)
	if !ok {
		return imageAttachment{}, fmt.Errorf("image format %q is not supported; use PNG, JPEG, GIF, or WebP", format)
	}
	return imageAttachment{
		Filename: cleanAttachmentFilename(filename), MediaType: mediaType, Format: label,
		Width: config.Width, Height: config.Height, Size: int64(len(data)), Data: append([]byte(nil), data...),
	}, nil
}

func imageAttachmentsFromPaths(paths []string, limits core.AttachmentLimits) ([]imageAttachment, int, error) {
	limits = normalizeAttachmentLimits(limits)
	attachments := make([]imageAttachment, 0, len(paths))
	skipped := 0
	totalBytes := int64(0)
	for _, path := range paths {
		if !hasSupportedImageExtension(path) {
			skipped++
			continue
		}
		if len(attachments) >= limits.MaxFiles {
			return nil, skipped, fmt.Errorf("too many images; attachments.maxFiles allows %d", limits.MaxFiles)
		}
		remaining := int64(0)
		if limits.MaxTotalBytes > 0 {
			remaining = limits.MaxTotalBytes - totalBytes
			if remaining <= 0 {
				return nil, skipped, fmt.Errorf("images exceed the per-message limit of %s", formatAttachmentBytes(limits.MaxTotalBytes))
			}
		}
		fileLimit := limits.MaxFileBytes
		if remaining > 0 && (fileLimit <= 0 || remaining < fileLimit) {
			fileLimit = remaining
		}
		attachment, err := imageAttachmentFromPath(path, fileLimit)
		if err != nil {
			return nil, skipped, err
		}
		if limits.MaxFileBytes > 0 && attachment.Size > limits.MaxFileBytes {
			return nil, skipped, fmt.Errorf("%s is %s; per-image limit is %s", filepath.Base(path), formatAttachmentBytes(attachment.Size), formatAttachmentBytes(limits.MaxFileBytes))
		}
		if limits.MaxTotalBytes > 0 && (attachment.Size > remaining || totalBytes > limits.MaxTotalBytes-attachment.Size) {
			return nil, skipped, fmt.Errorf("images exceed the per-message limit of %s", formatAttachmentBytes(limits.MaxTotalBytes))
		}
		totalBytes += attachment.Size
		attachments = append(attachments, attachment)
	}
	return attachments, skipped, nil
}

func imageAttachmentFromPath(path string, byteLimit int64) (imageAttachment, error) {
	file, err := os.Open(path)
	if err != nil {
		return imageAttachment{}, fmt.Errorf("open image %q: %w", filepath.Base(path), err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return imageAttachment{}, fmt.Errorf("inspect image %q: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return imageAttachment{}, fmt.Errorf("%q is not a regular image file", filepath.Base(path))
	}
	if byteLimit > 0 && info.Size() > byteLimit {
		return imageAttachment{}, fmt.Errorf("%s is %s; allowed limit is %s", filepath.Base(path), formatAttachmentBytes(info.Size()), formatAttachmentBytes(byteLimit))
	}
	reader := io.Reader(file)
	if byteLimit > 0 {
		reader = io.LimitReader(file, byteLimit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return imageAttachment{}, fmt.Errorf("read image %q: %w", filepath.Base(path), err)
	}
	if byteLimit > 0 && int64(len(data)) > byteLimit {
		return imageAttachment{}, fmt.Errorf("%s is larger than the allowed limit of %s", filepath.Base(path), formatAttachmentBytes(byteLimit))
	}
	attachment, err := imageAttachmentFromBytes(data, filepath.Base(path), core.AttachmentLimits{})
	if err != nil {
		return imageAttachment{}, fmt.Errorf("image %q: %w", filepath.Base(path), err)
	}
	return attachment, nil
}

func imageFormat(format string) (mediaType, label string, ok bool) {
	switch strings.ToLower(format) {
	case "png":
		return "image/png", "PNG", true
	case "jpeg":
		return "image/jpeg", "JPEG", true
	case "gif":
		return "image/gif", "GIF", true
	case "webp":
		return "image/webp", "WebP", true
	default:
		return "", "", false
	}
}

func hasSupportedImageExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func cleanAttachmentFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || character == '[' || character == ']' {
			return -1
		}
		return character
	}, value)
	if value == "." || value == string(filepath.Separator) || value == "" {
		return "clipboard image"
	}
	return value
}

func imageAttachmentToken(index int, attachment imageAttachment) string {
	return fmt.Sprintf("[Image #%d · %s · %d×%d · %s]", index, attachment.Format, attachment.Width, attachment.Height, formatAttachmentBytes(attachment.Size))
}

func formatAttachmentBytes(value int64) string {
	switch {
	case value >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(value)/(1<<20))
	case value >= 1<<10:
		return fmt.Sprintf("%d KiB", value/(1<<10))
	default:
		return fmt.Sprintf("%d B", value)
	}
}

func parsePastedImagePaths(value, workspace string) ([]string, bool) {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	var paths []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		linePaths, ok := splitPastedPathLine(line)
		if !ok {
			return nil, false
		}
		for _, linePath := range linePaths {
			path, ok := normalizePastedImagePath(linePath, workspace)
			if !ok || !hasSupportedImageExtension(path) {
				return nil, false
			}
			paths = append(paths, path)
		}
	}
	return paths, len(paths) > 0
}

func splitPastedPathLine(line string) ([]string, bool) {
	if strings.HasPrefix(strings.ToLower(line), "file://") {
		return []string{line}, true
	}
	quotedToken := false
	for index := 0; index < len(line); index++ {
		if (line[index] == '\'' || line[index] == '"') && (index == 0 || line[index-1] == ' ' || line[index-1] == '\t') {
			quotedToken = true
			break
		}
	}
	if !quotedToken {
		if !looksLikeImagePath(line) {
			return nil, false
		}
		return []string{line}, true
	}
	var paths []string
	for index := 0; index < len(line); {
		for index < len(line) && (line[index] == ' ' || line[index] == '\t') {
			index++
		}
		if index >= len(line) {
			break
		}
		quote := byte(0)
		if line[index] == '\'' || line[index] == '"' {
			quote = line[index]
			index++
		}
		start := index
		if quote != 0 {
			for index < len(line) && line[index] != quote {
				index++
			}
			if index == len(line) {
				return nil, false
			}
			paths = append(paths, line[start:index])
			index++
			if index < len(line) && line[index] != ' ' && line[index] != '\t' {
				return nil, false
			}
			continue
		}
		for index < len(line) && line[index] != ' ' && line[index] != '\t' {
			index++
		}
		paths = append(paths, line[start:index])
	}
	if len(paths) == 0 {
		return nil, false
	}
	for _, path := range paths {
		if !looksLikeImagePath(path) && !strings.HasPrefix(strings.ToLower(path), "file://") {
			return nil, false
		}
	}
	return paths, true
}

func looksLikeImagePath(value string) bool {
	value = strings.TrimSpace(value)
	if !hasSupportedImageExtension(value) || strings.ContainsAny(value, "\n\r") {
		return false
	}
	if filepath.IsAbs(value) || isWindowsAbsolutePath(value) || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") ||
		strings.HasPrefix(value, "~") || strings.ContainsAny(value, `/\`) {
		return true
	}
	return !strings.ContainsAny(value, " \t")
}

func normalizePastedImagePath(value, workspace string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "file://") {
		parsed, err := url.Parse(value)
		if err != nil || !strings.EqualFold(parsed.Scheme, "file") {
			return "", false
		}
		path, err := url.PathUnescape(parsed.EscapedPath())
		if err != nil {
			return "", false
		}
		if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
			path = `\\` + parsed.Host + filepath.FromSlash(path)
		} else if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		value = path
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, value[2:])
		}
	}
	if !filepath.IsAbs(value) && !isWindowsAbsolutePath(value) && workspace != "" {
		value = filepath.Join(workspace, value)
	}
	return filepath.Clean(value), true
}

func isWindowsAbsolutePath(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) &&
		value[1] == ':' && (value[2] == '\\' || value[2] == '/') || strings.HasPrefix(value, `\\`)
}

func buildTaskInputWithImages(text string, attachments []imageAttachment) (core.TaskInput, error) {
	type contentPart struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL string `json:"image_url,omitempty"`
	}
	type message struct {
		Type    string        `json:"type"`
		Role    string        `json:"role"`
		Content []contentPart `json:"content"`
	}
	type occurrence struct {
		start      int
		end        int
		attachment imageAttachment
	}
	occurrences := make([]occurrence, 0, len(attachments))
	for _, attachment := range attachments {
		index := strings.Index(text, attachment.Token)
		if index < 0 {
			return core.TaskInput{}, fmt.Errorf("image attachment placeholder %q is missing from the prompt", attachment.Token)
		}
		occurrences = append(occurrences, occurrence{start: index, end: index + len(attachment.Token), attachment: attachment})
	}
	if len(occurrences) == 0 {
		return core.NewTaskInput(text)
	}
	// Sort by their position in the editable prompt so model-visible image order
	// matches the order displayed in the composer.
	for i := 1; i < len(occurrences); i++ {
		for j := i; j > 0 && occurrences[j].start < occurrences[j-1].start; j-- {
			occurrences[j], occurrences[j-1] = occurrences[j-1], occurrences[j]
		}
	}
	parts := make([]contentPart, 0, len(occurrences)*2+1)
	cursor := 0
	for _, occurrence := range occurrences {
		if occurrence.start < cursor {
			continue
		}
		if prefix := text[cursor:occurrence.start]; prefix != "" {
			parts = append(parts, contentPart{Type: "input_text", Text: prefix})
		}
		parts = append(parts,
			contentPart{Type: "input_text", Text: occurrence.attachment.Token},
			contentPart{Type: "input_image", ImageURL: "data:" + occurrence.attachment.MediaType + ";base64," + base64.StdEncoding.EncodeToString(occurrence.attachment.Data)},
		)
		cursor = occurrence.end
	}
	if suffix := text[cursor:]; suffix != "" {
		parts = append(parts, contentPart{Type: "input_text", Text: suffix})
	}
	return core.NewTaskInput([]message{{Type: "message", Role: "user", Content: parts}})
}

func shallowCopyImageAttachments(attachments []imageAttachment) []imageAttachment {
	return append([]imageAttachment(nil), attachments...)
}

func draftTaskInput(text string, attachments []imageAttachment) (core.TaskInput, error) {
	if len(attachments) == 0 {
		return parseInputLine(text)
	}
	return buildTaskInputWithImages(text, attachments)
}

func (m *tuiModel) canAttachImages() bool {
	return !m.keymapOpen && !m.detailsOpen && !m.sessionPicker && !m.renamingSession &&
		!m.queuePicker && !m.permissionMenu && !m.modelPicker && !m.effortPicker && m.pendingApproval == nil
}

func (m *tuiModel) readClipboardInput() tea.Cmd {
	reader := m.clipboardReader
	textReader := m.clipboardTextReader
	workspace := m.workspace
	limits := normalizeAttachmentLimits(m.attachmentLimits)
	return func() tea.Msg {
		if reader == nil {
			reader = systemClipboardImageReader{}
		}
		if textReader == nil {
			textReader = readClipboardText
		}
		fallback := ""
		textRead := false
		readFallback := func() string {
			if !textRead {
				fallback, _ = textReader()
				textRead = true
			}
			return fallback
		}
		ctx, cancel := clipboardReadContext()
		defer cancel()
		payload, readErr := reader.Read(ctx)

		skipped := 0
		if readErr == nil && len(payload.Files) > 0 {
			attachments, ignored, err := imageAttachmentsFromPaths(payload.Files, limits)
			skipped += ignored
			if err != nil {
				return imageInputReadyMsg{FallbackText: readFallback(), Skipped: skipped, Err: err}
			}
			if len(attachments) > 0 {
				return imageInputReadyMsg{Attachments: attachments, Skipped: skipped}
			}
		}
		if readErr == nil && len(payload.Image) > 0 {
			if int64(len(payload.Image)) > limits.MaxTotalBytes {
				return imageInputReadyMsg{FallbackText: readFallback(), Skipped: skipped,
					Err: fmt.Errorf("image exceeds the per-message limit of %s", formatAttachmentBytes(limits.MaxTotalBytes))}
			}
			attachment, err := imageAttachmentFromBytes(payload.Image, "clipboard image", limits)
			if err != nil {
				return imageInputReadyMsg{FallbackText: readFallback(), Skipped: skipped, Err: err}
			}
			return imageInputReadyMsg{Attachments: []imageAttachment{attachment}, Skipped: skipped}
		}

		fallback = readFallback()
		if paths, ok := parsePastedImagePaths(fallback, workspace); ok {
			attachments, ignored, err := imageAttachmentsFromPaths(paths, limits)
			return imageInputReadyMsg{Attachments: attachments, FallbackText: fallback, Skipped: ignored, Err: err}
		}
		return imageInputReadyMsg{FallbackText: fallback, Skipped: skipped, Err: readErr}
	}
}

func (m *tuiModel) loadPastedImagePaths(paths []string, sourceText string) tea.Cmd {
	return loadPastedImagePathsCmd(paths, sourceText, normalizeAttachmentLimits(m.attachmentLimits))
}

func loadPastedImagePathsCmd(paths []string, sourceText string, limits core.AttachmentLimits) tea.Cmd {
	paths = append([]string(nil), paths...)
	return func() tea.Msg {
		attachments, skipped, err := imageAttachmentsFromPaths(paths, limits)
		return imageInputReadyMsg{Attachments: attachments, FallbackText: sourceText, Skipped: skipped, Err: err}
	}
}

func (m *tuiModel) applyImageInputResult(result imageInputReadyMsg) {
	if !m.canAttachImages() {
		m.status = "Image paste was ignored because the composer is inactive."
		return
	}
	if len(result.Attachments) > 0 {
		if err := m.insertImageAttachments(result.Attachments); err != nil {
			m.status = "Image attachment was not added: " + err.Error()
			if result.FallbackText != "" {
				m.insertPastedText(result.FallbackText)
			}
			return
		}
		m.status = fmt.Sprintf("Added %d image(s) to the draft.", len(result.Attachments))
		if result.Skipped > 0 {
			m.status += fmt.Sprintf(" Skipped %d non-image item(s).", result.Skipped)
		}
		return
	}
	if result.FallbackText != "" {
		m.insertPastedText(result.FallbackText)
		if result.Err != nil {
			m.status = "Image could not be attached: " + result.Err.Error() + "; pasted content remains in the draft."
		} else if result.Skipped > 0 {
			m.status = fmt.Sprintf("No supported images found; pasted content remains in the draft (%d item(s) skipped).", result.Skipped)
		}
		return
	}
	if result.Err != nil {
		m.status = "Image attachment failed: " + result.Err.Error()
		return
	}
	if result.Skipped > 0 {
		m.status = fmt.Sprintf("No supported images found in the clipboard (%d item(s) skipped).", result.Skipped)
	}
}

func (m *tuiModel) insertPastedText(value string) {
	_ = m.updateInput(tea.PasteMsg{Content: value})
	m.updateCommandSuggestions()
}

func (m *tuiModel) insertImageAttachments(incoming []imageAttachment) error {
	combined := append(shallowCopyImageAttachments(m.attachments), incoming...)
	if err := validateImageAttachmentSet(combined, m.attachmentLimits); err != nil {
		return err
	}
	if len(incoming) == 0 {
		return nil
	}
	before := m.input.Value()
	m.pushInputUndoSnapshot(before)
	m.input.DeleteSelection()
	separator := ""
	if strings.TrimSpace(m.input.Value()) != "" {
		separator = " "
	}
	var tokens strings.Builder
	for index, attachment := range incoming {
		if index > 0 {
			tokens.WriteByte(' ')
		}
		attachment.Token = imageAttachmentToken(m.nextAttachmentID, attachment)
		m.nextAttachmentID++
		incoming[index] = attachment
		tokens.WriteString(attachment.Token)
	}
	m.input.InsertString(separator + tokens.String() + " ")
	m.attachments = append(m.attachments, incoming...)
	m.redoStack = nil
	after := m.input.Value()
	if before == after {
		m.undoStack = m.undoStack[:len(m.undoStack)-1]
	}
	m.updateCommandSuggestions()
	return nil
}

func (m *tuiModel) pushInputUndoSnapshot(text string) {
	m.undoStack = append(m.undoStack, inputSnapshot{text: text, attachments: shallowCopyImageAttachments(m.attachments)})
	if len(m.undoStack) > maxInputUndoSteps {
		m.undoStack = m.undoStack[len(m.undoStack)-maxInputUndoSteps:]
	}
}

func (m *tuiModel) restoreInputSnapshot(snapshot inputSnapshot) {
	m.input.SetValue(snapshot.text)
	m.attachments = shallowCopyImageAttachments(snapshot.attachments)
	m.nextAttachmentID = nextImageAttachmentID(m.attachments)
}

func (m *tuiModel) reconcileDraftAttachments(value string) {
	kept := m.attachments[:0]
	for _, attachment := range m.attachments {
		if strings.Contains(value, attachment.Token) {
			kept = append(kept, attachment)
		}
	}
	m.attachments = kept
	if m.nextAttachmentID < nextImageAttachmentID(m.attachments) {
		m.nextAttachmentID = nextImageAttachmentID(m.attachments)
	}
}

func nextImageAttachmentID(attachments []imageAttachment) int {
	next := 1
	for _, attachment := range attachments {
		var index int
		if _, err := fmt.Sscanf(attachment.Token, "[Image #%d", &index); err == nil && index >= next {
			next = index + 1
		}
	}
	return next
}

func (m *tuiModel) renderInputView() string {
	view := m.input.View()
	if len(m.attachments) == 0 {
		return view
	}
	colors := colorsFor(m.preferences.Theme)
	chip := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Background)).Background(lipgloss.Color(colors.Accent))
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Background(lipgloss.Color(colors.Background)).Render("x")
	basePrefix := ""
	if marker := strings.Index(base, "x"); marker >= 0 {
		basePrefix = base[:marker]
	}
	for _, attachment := range m.attachments {
		view = strings.Replace(view, attachment.Token, chip.Render(attachment.Token)+basePrefix, 1)
	}
	return view
}

func clipboardReadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
