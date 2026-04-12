package ingestion

import "encoding/json"

// MetaPayload is the optional sidecar metadata that can accompany any ingested file.
// For single files: uploaded as {filename}.meta alongside the source file.
// For folder jobs: embedded in _READY.json as the root object.
type MetaPayload struct {
	JobAlias     *string           `json:"job_alias"`
	Priority     *int              `json:"priority"`
	UserData     *string           `json:"user_data"`
	CustomFields map[string]string `json:"custom_fields"`
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
