package service

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/tidwall/gjson"
)

type CodexAPIInspection struct {
	Body     []byte
	Model    string
	Protocol string
}

func InspectCodexAPIRequest(target, contentType string, body []byte) (CodexAPIInspection, error) {
	result := CodexAPIInspection{Body: body, Model: gjson.GetBytes(body, "model").String(), Protocol: CodexAPIInspectionProtocol(target)}
	if result.Model == "" {
		result.Model = gjson.GetBytes(body, "session.model").String()
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		return result, nil
	}
	if result.Protocol == ContentModerationProtocolOpenAIImages {
		parsed := &OpenAIImagesRequest{}
		if err := parseOpenAIImagesMultipartRequest(body, contentType, parsed); err != nil {
			return result, err
		}
		result.Body, result.Model = parsed.ModerationBody(), parsed.Model
		return result, nil
	}
	fields := make(map[string]json.RawMessage)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		value, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			return result, err
		}
		if part.FileName() != "" {
			continue
		}
		switch part.FormName() {
		case "model", "prompt":
			fields[part.FormName()], err = json.Marshal(string(value))
		case "session":
			if json.Valid(value) {
				fields["session"] = value
			}
		}
		if err != nil {
			return result, err
		}
	}
	result.Body, err = json.Marshal(fields)
	result.Model = gjson.GetBytes(result.Body, "model").String()
	if result.Model == "" {
		result.Model = gjson.GetBytes(result.Body, "session.model").String()
	}
	return result, err
}

func CodexAPIInspectionProtocol(target string) string {
	if strings.HasSuffix(target, "/images/generations") || strings.HasSuffix(target, "/images/edits") {
		return ContentModerationProtocolOpenAIImages
	}
	if strings.HasSuffix(target, "/realtime/calls") || strings.HasSuffix(target, "/realtime") || strings.HasSuffix(target, "/live") || strings.Contains(target, "/live/") {
		return ContentModerationProtocolOpenAIRealtime
	}
	return ContentModerationProtocolOpenAIResponses
}
