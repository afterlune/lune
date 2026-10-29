package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
	_ "golang.org/x/image/webp"
)

type ImageInput struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type SubmitInput struct {
	Text             string       `json:"text"`
	Images           []ImageInput `json:"images"`
	UserMessageIndex *int         `json:"userMessageIndex,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type userMessage struct {
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

func (s *DesktopService) Submit(sessionID string, input SubmitInput) (core.TurnSubmission, error) {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	defer release()
	if sessionContext.runtime == nil || sessionContext.core == nil {
		return core.TurnSubmission{}, errors.New("请先打开会话")
	}
	taskInput, err := s.buildTaskInput(sessionContext, input)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	state, err := sessionContext.runtime.State(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	if state.Task.Status == core.TaskPaused {
		return core.TurnSubmission{}, errors.New("请先处理当前等待中的请求")
	}
	mode := core.StartIfIdle
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || len(state.Queue) > 0 {
		mode = core.QueueInput
	}
	return s.submitPreparedLocked(sessionContext, taskInput, input, mode)
}

func (s *DesktopService) EditUserMessage(sessionID string, messageID string, input SubmitInput) (core.TurnSubmission, error) {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	defer release()
	if sessionContext.runtime == nil || sessionContext.core == nil {
		return core.TurnSubmission{}, errors.New("请先打开会话")
	}
	if strings.TrimSpace(messageID) == "" {
		return core.TurnSubmission{}, errors.New("用户消息标识无效")
	}
	taskInput, err := s.buildTaskInput(sessionContext, input)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	state, err := sessionContext.runtime.State(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	pending, err := sessionContext.runtime.PendingRequests(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	if len(pending) > 0 || state.Task.Status == core.TaskPaused {
		return core.TurnSubmission{}, errors.New("请先处理当前等待中的请求")
	}
	saved, err := sessionContext.core.GetSession(context.Background(), sessionContext.sessionID)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	conversation := transcriptMessages(saved.History)
	userMessageIndex := findTranscriptUserMessageIndex(conversation, messageID)
	if userMessageIndex >= 0 {
		return s.editConversationFromIndexLocked(sessionContext, messageID, userMessageIndex, input, taskInput)
	}

	queuedTarget := core.QueuedTurnState{}
	queuedFound := false
	for _, item := range state.Queue {
		if item.ExecutionID == messageID {
			queuedTarget, queuedFound = item, true
			break
		}
	}
	activityIndex := input.UserMessageIndex
	if s.activityStore != nil {
		runs, listErr := s.activityStore.ListSession(context.Background(), sessionContext.sessionID)
		if listErr != nil {
			return core.TurnSubmission{}, listErr
		}
		for _, run := range runs {
			if run.ExecutionID == messageID {
				index := run.UserMessageIndex
				activityIndex = &index
				break
			}
		}
	}
	if !queuedFound && activityIndex == nil {
		return core.TurnSubmission{}, errors.New("要编辑的用户消息已不在当前会话历史或任务队列中")
	}
	if activityIndex == nil || *activityIndex < 0 {
		return core.TurnSubmission{}, errors.New("无法确定排队消息在会话中的位置")
	}
	if queuedFound && queuedTarget.Status == "queued" {
		submission, cancelled, replaceErr := sessionContext.runtime.ReplaceQueuedTurnInput(context.Background(), messageID, taskInput)
		if replaceErr == nil {
			index := *activityIndex
			input.UserMessageIndex = &index
			if s.activityStore != nil {
				if err := s.activityStore.AssociateExecution(context.Background(), sessionContext.sessionID, submission.ExecutionID, index, "queued"); err != nil {
					slog.Warn("update edited queued execution", "error", err)
				}
				images := imageDataURLs(input.Images)
				if err := s.activityStore.SaveExecutionMessage(context.Background(), sessionContext.sessionID, submission.ExecutionID, strings.TrimSpace(input.Text), images, "queued", submission.Position); err != nil {
					slog.Warn("update edited queued message", "error", err)
				}
				if err := s.activityStore.DeleteFromUserMessageIndexExcept(context.Background(), sessionContext.sessionID, index+1, ""); err != nil {
					slog.Warn("remove queue entries after edited message", "error", err)
				} else {
					s.suppressExecutionActivity(cancelled)
				}
			} else {
				s.suppressExecutionActivity(cancelled)
			}
			return submission, nil
		}
		if !errors.Is(replaceErr, core.ErrQueuedTurnStarted) && !errors.Is(replaceErr, core.ErrTurnNotFound) {
			return core.TurnSubmission{}, replaceErr
		}
	}
	return s.editConversationFromIndexLocked(sessionContext, "", *activityIndex, input, taskInput)
}

func (s *DesktopService) CancelQueuedTurn(sessionID string, executionID string) error {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	if sessionContext.runtime == nil || sessionContext.core == nil {
		return errors.New("请先打开会话")
	}
	return sessionContext.runtime.CancelQueuedTurn(context.Background(), strings.TrimSpace(executionID))
}

func findTranscriptUserMessageIndex(messages []ConversationMessage, messageID string) int {
	userIndex := 0
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		if message.ID == messageID {
			return userIndex
		}
		userIndex++
	}
	return -1
}

func (s *DesktopService) editConversationFromIndexLocked(sessionContext *desktopSessionContext, messageID string, requestedIndex int, input SubmitInput, taskInput core.TaskInput) (core.TurnSubmission, error) {
	state, err := sessionContext.runtime.State(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	pending, err := sessionContext.runtime.PendingRequests(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	if len(pending) > 0 || state.Task.Status == core.TaskPaused {
		return core.TurnSubmission{}, errors.New("请先处理当前等待中的请求")
	}
	var affected []string
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || len(state.Queue) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		affected, err = sessionContext.runtime.CancelPendingTurns(ctx)
		cancel()
		if err != nil {
			return core.TurnSubmission{}, fmt.Errorf("中断当前任务并清空后续队列失败: %w", err)
		}
	}
	saved, err := sessionContext.core.GetSession(context.Background(), sessionContext.sessionID)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	conversation := transcriptMessages(saved.History)
	userMessageCount := 0
	for _, message := range conversation {
		if message.Role == "user" {
			userMessageCount++
		}
	}
	userMessageIndex := requestedIndex
	if messageID != "" {
		if resolved := findTranscriptUserMessageIndex(conversation, messageID); resolved >= 0 {
			userMessageIndex = resolved
		}
	}
	if userMessageIndex < 0 || userMessageIndex > userMessageCount || (userMessageIndex == userMessageCount && len(affected) == 0) {
		return core.TurnSubmission{}, errors.New("要编辑的用户消息已不在当前会话历史中")
	}
	originalHistory := append(json.RawMessage(nil), saved.History...)
	rewound := false
	if userMessageIndex < userMessageCount {
		removeCount := userMessageCount - userMessageIndex
		removed, err := sessionContext.core.RewindSession(context.Background(), sessionContext.sessionID, removeCount)
		if err != nil {
			return core.TurnSubmission{}, err
		}
		if removed != removeCount {
			_ = sessionContext.core.SetSessionHistory(context.Background(), sessionContext.sessionID, originalHistory)
			return core.TurnSubmission{}, errors.New("回退会话历史时消息数量发生变化")
		}
		rewound = true
	}
	input.UserMessageIndex = &userMessageIndex
	submission, err := s.submitPreparedLocked(sessionContext, taskInput, input, core.StartIfIdle)
	if err != nil {
		if rewound {
			err = errors.Join(err, sessionContext.core.SetSessionHistory(context.Background(), sessionContext.sessionID, originalHistory))
		}
		return core.TurnSubmission{}, err
	}
	if s.activityStore != nil {
		if err := s.activityStore.DeleteFromUserMessageIndexExcept(context.Background(), sessionContext.sessionID, userMessageIndex, submission.ExecutionID); err != nil {
			slog.Warn("remove execution history after editing a user message", "error", err)
		} else {
			s.suppressExecutionActivity(affected)
		}
	} else {
		s.suppressExecutionActivity(affected)
	}
	return submission, nil
}

func imageDataURLs(images []ImageInput) []string {
	result := make([]string, 0, len(images))
	for _, item := range images {
		result = append(result, "data:"+item.MediaType+";base64,"+item.Data)
	}
	return result
}

func (s *DesktopService) buildTaskInput(sessionContext *desktopSessionContext, input SubmitInput) (core.TaskInput, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" && len(input.Images) == 0 {
		return core.TaskInput{}, errors.New("请输入内容或添加图片")
	}
	var taskInput core.TaskInput
	var err error
	if len(input.Images) == 0 {
		taskInput, err = core.NewTaskInput(text)
	} else {
		if sessionContext.core == nil {
			return core.TaskInput{}, errors.New("请先打开工作区")
		}
		limits := sessionContext.core.ProjectAttachmentLimits()
		if len(input.Images) > limits.MaxFiles {
			return core.TaskInput{}, fmt.Errorf("图片数量超出限制（最多 %d 张）", limits.MaxFiles)
		}
		parts := make([]contentPart, 0, len(input.Images)*2+1)
		if text != "" {
			parts = append(parts, contentPart{Type: "input_text", Text: text})
		}
		var total int64
		for _, item := range input.Images {
			if item.MediaType != "image/png" && item.MediaType != "image/jpeg" && item.MediaType != "image/gif" && item.MediaType != "image/webp" {
				return core.TaskInput{}, fmt.Errorf("不支持的图片格式: %s", item.MediaType)
			}
			data, decodeErr := base64.StdEncoding.DecodeString(item.Data)
			if decodeErr != nil {
				return core.TaskInput{}, fmt.Errorf("读取图片 %q: %w", item.Name, decodeErr)
			}
			if int64(len(data)) > limits.MaxFileBytes {
				return core.TaskInput{}, fmt.Errorf("图片 %q 超出单文件大小限制", item.Name)
			}
			total += int64(len(data))
			if total > limits.MaxTotalBytes {
				return core.TaskInput{}, errors.New("图片总大小超出限制")
			}
			config, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
			if decodeErr != nil || config.Width < 1 || config.Height < 1 || !supportedImageFormat(format) {
				return core.TaskInput{}, fmt.Errorf("图片 %q 无法解码", item.Name)
			}
			parts = append(parts, contentPart{Type: "input_image", ImageURL: "data:" + item.MediaType + ";base64," + base64.StdEncoding.EncodeToString(data)})
		}
		taskInput, err = core.NewTaskInput([]userMessage{{Type: "message", Role: "user", Content: parts}})
	}
	if err != nil {
		return core.TaskInput{}, err
	}
	return taskInput, nil
}

func (s *DesktopService) submitPreparedLocked(sessionContext *desktopSessionContext, taskInput core.TaskInput, input SubmitInput, mode core.SubmissionMode) (core.TurnSubmission, error) {
	controls := s.controlsFor(sessionContext)
	request := core.TurnRequest{Input: taskInput, PlanMode: controls.PlanMode, PermissionMode: core.PermissionMode(controls.EffectivePermissionMode)}
	submission, err := sessionContext.runtime.SubmitTurn(context.Background(), request, mode)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	messageIndex := userMessageIndex(input.UserMessageIndex, sessionContext.core, sessionContext.sessionID)
	if s.activityStore != nil {
		status := "running"
		if submission.Status == core.SubmissionQueued {
			status = "queued"
		}
		if err := s.activityStore.AssociateExecution(context.Background(), sessionContext.sessionID, submission.ExecutionID, messageIndex, status); err != nil {
			slog.Warn("save desktop execution activity", "error", err)
		}
		images := make([]string, 0, len(input.Images))
		for _, item := range input.Images {
			images = append(images, "data:"+item.MediaType+";base64,"+item.Data)
		}
		if err := s.activityStore.SaveExecutionMessage(context.Background(), sessionContext.sessionID, submission.ExecutionID, strings.TrimSpace(input.Text), images, status, submission.Position); err != nil {
			slog.Warn("save desktop queued message", "error", err)
		}
	}
	s.mu.Lock()
	s.watchTurnLocked(sessionContext.runtime, sessionContext.sessionID, submission.ExecutionID)
	s.mu.Unlock()
	return submission, nil
}

func userMessageIndex(requested *int, agent *core.Core, sessionID string) int {
	if requested != nil && *requested >= 0 {
		return *requested
	}
	if agent == nil || sessionID == "" {
		return -1
	}
	saved, err := agent.GetSession(context.Background(), sessionID)
	if err != nil {
		return -1
	}
	index := 0
	for _, message := range transcriptMessages(saved.History) {
		if message.Role == "user" {
			index++
		}
	}
	return index
}

func supportedImageFormat(format string) bool {
	switch strings.ToLower(format) {
	case "png", "jpeg", "gif", "webp":
		return true
	default:
		return false
	}
}

func (s *DesktopService) Interrupt(sessionID, expectedExecutionID string) bool {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return false
	}
	defer release()
	runtime := sessionContext.runtime
	if runtime == nil {
		return false
	}
	state, err := runtime.State(context.Background())
	if err != nil {
		return false
	}
	if state.Task.ExecutionID != expectedExecutionID {
		return false
	}
	if state.Task.Status == core.TaskPaused {
		discarded, err := runtime.DiscardPaused(context.Background())
		if err == nil && discarded {
			saveInterruptedActivity(s, runtime.SessionID(), state.Task.ExecutionID)
		}
		return err == nil && discarded
	}
	if state.Task.Status != core.TaskRunning && state.Task.Status != core.TaskQueued {
		return false
	}
	executionID := state.Task.ExecutionID
	accepted, interruptErr := runtime.Interrupt(context.Background(), executionID)
	if !accepted || interruptErr != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	result, err := runtime.WaitTurn(ctx, executionID)
	if err != nil {
		return false
	}
	if result.Status == core.TaskPaused {
		discarded, discardErr := runtime.DiscardPaused(ctx)
		if discardErr != nil || !discarded {
			return false
		}
		saveInterruptedActivity(s, runtime.SessionID(), executionID)
	} else if result.Status == core.TaskCancelled {
		saveInterruptedActivity(s, runtime.SessionID(), executionID)
	}
	return true
}

func saveInterruptedActivity(service *DesktopService, sessionID, executionID string) {
	if service == nil || sessionID == "" || executionID == "" {
		return
	}
	service.mu.Lock()
	store := service.activityStore
	service.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.UpsertExecution(context.Background(), sessionID, executionID, nil, "interrupted"); err != nil {
		slog.Warn("save interrupted execution activity", "error", err)
	}
	if err := store.UpdateExecutionMessageStatus(context.Background(), sessionID, executionID, "interrupted", 0); err != nil {
		slog.Warn("remove interrupted desktop message receipt", "error", err)
	}
}
