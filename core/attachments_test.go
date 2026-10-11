package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	projectconfig "github.com/afterlune/lune/internal/config"
	"github.com/afterlune/lune/internal/model"
)

func TestValidateTaskInputAttachmentLimits(t *testing.T) {
	dataURL := func(value string) string {
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(value))
	}
	tests := []struct {
		name    string
		images  []model.InputImage
		limits  projectconfig.AttachmentLimits
		wantErr string
	}{
		{
			name:   "within per-file and total limits",
			images: []model.InputImage{{ImageURL: dataURL("1234")}, {ImageURL: dataURL("567")}},
			limits: projectconfig.AttachmentLimits{MaxFiles: 2, MaxFileBytes: 4, MaxTotalBytes: 7},
		},
		{
			name:   "case-insensitive data URL header",
			images: []model.InputImage{{ImageURL: "DATA:IMAGE/PNG;BASE64,MTIz"}},
			limits: projectconfig.AttachmentLimits{MaxFileBytes: 3},
		},
		{
			name:   "URL-escaped base64 payload",
			images: []model.InputImage{{ImageURL: "data:image/png;base64,%2B%2F8%3D"}},
			limits: projectconfig.AttachmentLimits{MaxFileBytes: 2},
		},
		{
			name:    "malformed URL escape",
			images:  []model.InputImage{{ImageURL: "data:image/png;base64,%2G%2F8%3D"}},
			wantErr: "valid base64",
		},
		{
			name:    "malformed URL escape after base64 padding",
			images:  []model.InputImage{{ImageURL: "data:image/png;base64,YQ==%2G"}},
			wantErr: "valid base64",
		},
		{
			name:    "too many images",
			images:  []model.InputImage{{ImageURL: dataURL("1")}, {ImageURL: dataURL("2")}},
			limits:  projectconfig.AttachmentLimits{MaxFiles: 1},
			wantErr: "attachments.maxFiles",
		},
		{
			name:    "file byte limit",
			images:  []model.InputImage{{ImageURL: dataURL("12345")}},
			limits:  projectconfig.AttachmentLimits{MaxFileBytes: 4},
			wantErr: "attachments.maxFileBytes",
		},
		{
			name:    "total byte limit",
			images:  []model.InputImage{{ImageURL: dataURL("1234")}, {ImageURL: dataURL("5678")}},
			limits:  projectconfig.AttachmentLimits{MaxTotalBytes: 7},
			wantErr: "attachments.maxTotalBytes",
		},
		{
			name:    "malformed base64",
			images:  []model.InputImage{{ImageURL: "data:image/png;base64,not base64!"}},
			wantErr: "valid base64",
		},
		{
			name:    "remote image cannot satisfy byte limits",
			images:  []model.InputImage{{ImageURL: "https://example.test/image.png"}},
			limits:  projectconfig.AttachmentLimits{MaxTotalBytes: 1024},
			wantErr: "size cannot be verified",
		},
		{
			name:   "remote image with count-only limit",
			images: []model.InputImage{{FileID: "file-1"}},
			limits: projectconfig.AttachmentLimits{MaxFiles: 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := taskInputWithImages(test.images...)
			err := validateTaskInputAttachments(input, test.limits)
			if test.wantErr == "" && err != nil {
				t.Fatalf("validateTaskInputAttachments: %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("validation error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestRunTaskRejectsAttachmentsBeforeStartingWhenOverLimit(t *testing.T) {
	input, err := json.Marshal(taskInputWithImages(
		model.InputImage{ImageURL: "data:image/png;base64,YQ=="},
		model.InputImage{ImageURL: "data:image/png;base64,Yg=="},
	))
	if err != nil {
		t.Fatal(err)
	}
	client := &Core{attachmentLimits: projectconfig.AttachmentLimits{MaxFiles: 1}, active: TaskState{Status: TaskIdle}}
	_, err = client.RunTask(context.Background(), TaskRequest{SessionID: "not-started", Input: TaskInput{Raw: input}})
	if err == nil || !strings.Contains(err.Error(), "attachments.maxFiles") {
		t.Fatalf("RunTask validation error = %v, want attachment limit error", err)
	}
	if state := client.State(); state.Status != TaskIdle || state.TaskID != "" {
		t.Fatalf("task started before attachment validation: %#v", state)
	}
}

func taskInputWithImages(images ...model.InputImage) *model.ResponseInput {
	parts := make([]model.ContentPart, 0, len(images))
	for _, image := range images {
		parts = append(parts, image)
	}
	return model.NewResponseInputItems(model.InputMessage{
		Role: model.RoleUser, Content: model.NewMessageContentParts(parts...),
	})
}
