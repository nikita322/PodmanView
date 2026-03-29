package mqtt2http

import "time"

// OperatorType defines how multiple trigger conditions are combined
type OperatorType string

const (
	OperatorAND OperatorType = "AND"
	OperatorOR  OperatorType = "OR"
)

// ActionType defines the type of action to execute
type ActionType string

const (
	ActionHTTP ActionType = "http"
	ActionMQTT ActionType = "mqtt"
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

// MQTTAction defines the MQTT publish action to execute when triggered
type MQTTAction struct {
	Topic          string `json:"topic"`   // Target MQTT topic (supports templates)
	Payload        string `json:"payload"` // Message payload (supports templates)
	QoS            byte   `json:"qos"`     // 0, 1, or 2
	Retain         bool   `json:"retain"`
	AutoOffDelay   int    `json:"autoOffDelay,omitempty"`   // Seconds before sending auto-off payload (0 = disabled)
	AutoOffPayload string `json:"autoOffPayload,omitempty"` // Payload to send after delay (supports templates)
}

// HookBlock is a complete trigger-action unit
type HookBlock struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Enabled    bool         `json:"enabled"`
	Trigger    TriggerBlock `json:"trigger"`
	ActionType ActionType   `json:"actionType"`           // "http" or "mqtt"
	Action     HTTPAction   `json:"action"`               // Used when actionType == "http"
	MQTTAction MQTTAction   `json:"mqttAction,omitempty"` // Used when actionType == "mqtt"
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
