package ingestion

import (
	"encoding/json"
	"strconv"
	"strings"
)

// MetaPayload is the optional sidecar metadata that can accompany any ingested file.
// For single files: uploaded as {filename}.meta alongside the source file.
// For folder jobs: embedded in _READY.json as the root object.
type MetaPayload struct {
	JobAlias     *string           `json:"job_alias"`
	Priority     *FlexInt          `json:"priority"`
	UserData     *string           `json:"user_data"`
	CustomFields map[string]string `json:"custom_fields"`
}

// FlexInt unmarshals both JSON numbers (10) and JSON strings ("10").
// Clients often send priorities as quoted strings — this avoids a silent parse failure.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err == nil {
			*f = FlexInt(n)
		}
		return nil
	}
	return nil
}

// parseMeta decodes raw JSON into a MetaPayload.
// Returns nil, nil for empty input — normal case when no .meta file exists.
func parseMeta(data []byte) (*MetaPayload, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var p MetaPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
