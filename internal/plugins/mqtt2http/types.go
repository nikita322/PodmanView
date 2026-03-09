package mqtt2http

import "time"

// OperatorType defines how multiple trigger conditions are combined
type OperatorType string

const (
	OperatorAND OperatorType = "AND"
	OperatorOR  OperatorType = "OR"
)

// HTTPMethod represents supported HTTP methods
type HTTPMethod string

const (
	MethodGET    HTTPMethod = "GET"
	MethodPOST   HTTPMethod = "POST"
	MethodPUT    HTTPMethod = "PUT"
	MethodDELETE HTTPMethod = "DELETE"
	MethodPATCH  HTTPMethod = "PATCH"
)

// BodyType defines the format of HTTP request body
type BodyType string

const (
	BodyJSON    BodyType = "json"
	BodyForm    BodyType = "form"
	BodyRaw     BodyType = "raw"
	BodyForward BodyType = "forward" // Forward MQTT payload as-is
	BodyNone    BodyType = "none"
)

// TriggerMode defines when the condition fires
type TriggerMode string

const (
	// TriggerAlways fires on every matching message (level trigger)
	TriggerAlways TriggerMode = "always"
	// TriggerOnChange fires only when payload match state transitions from false to true (edge trigger)
	TriggerOnChange TriggerMode = "on_change"
)

// TriggerCondition represents a single MQTT topic match condition
type TriggerCondition struct {
	TopicPattern string      `json:"topicPattern"` // Regex pattern for topic matching
	PayloadRegex string      `json:"payloadRegex"` // Optional regex for payload filtering
	Mode         TriggerMode `json:"mode"`         // "always" or "on_change"
}

// TriggerBlock defines conditions that trigger an HTTP action
type TriggerBlock struct {
	Conditions []TriggerCondition `json:"conditions"`
	Operator   OperatorType       `json:"operator"` // AND or OR between conditions
}

// HTTPAction defines the HTTP request to execute when triggered
type HTTPAction struct {
	Method   HTTPMethod        `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	BodyType BodyType          `json:"bodyType"`
	Body     string            `json:"body"`    // Static body or template; ignored for "forward"
	Timeout  int               `json:"timeout"` // Seconds, default 10
}

// HookBlock is a complete trigger-action unit
type HookBlock struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Enabled bool         `json:"enabled"`
	Trigger TriggerBlock `json:"trigger"`
	Action  HTTPAction   `json:"action"`
}

// ExecutionLog records a single trigger execution
type ExecutionLog struct {
	BlockID      string    `json:"blockId"`
	BlockName    string    `json:"blockName"`
	Timestamp    time.Time `json:"timestamp"`
	TriggerTopic string    `json:"triggerTopic"`
	StatusCode   int       `json:"statusCode"`
	DurationMs   int64     `json:"durationMs"`
	Error        string    `json:"error,omitempty"`
}
