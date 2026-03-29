package reactor

import "time"

// OperatorType defines how multiple trigger conditions are combined
type OperatorType string

const (
	OperatorAND OperatorType = "AND"
	OperatorOR  OperatorType = "OR"
)

// TriggerMode defines when the condition fires
type TriggerMode string

const (
	TriggerAlways   TriggerMode = "always"
	TriggerOnChange TriggerMode = "on_change"
)

// TriggerCondition represents a single MQTT topic match condition
type TriggerCondition struct {
	TopicPattern string      `json:"topicPattern"`
	PayloadRegex string      `json:"payloadRegex"`
	Mode         TriggerMode `json:"mode"`
}

// TriggerBlock defines conditions that trigger the action pipeline
type TriggerBlock struct {
	Conditions []TriggerCondition `json:"conditions"`
	Operator   OperatorType       `json:"operator"`
}

// ActionType defines what kind of action to execute
type ActionType string

const (
	ActionHTTP  ActionType = "http"
	ActionMQTT  ActionType = "mqtt"
	ActionDelay ActionType = "delay"
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
	BodyForward BodyType = "forward"
	BodyNone    BodyType = "none"
)

// HTTPActionConfig defines an HTTP request action
type HTTPActionConfig struct {
	Method   HTTPMethod        `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	BodyType BodyType          `json:"bodyType"`
	Body     string            `json:"body"`
	Timeout  int               `json:"timeout"`
}

// MQTTActionConfig defines an MQTT publish action
type MQTTActionConfig struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	QoS     byte   `json:"qos"`
	Retain  bool   `json:"retain"`
}

// DelayActionConfig defines a delay (timer) action
type DelayActionConfig struct {
	Seconds int `json:"seconds"`
}

// Action is a single step in the action pipeline
type Action struct {
	Type  ActionType         `json:"type"`
	HTTP  *HTTPActionConfig  `json:"http,omitempty"`
	MQTT  *MQTTActionConfig  `json:"mqtt,omitempty"`
	Delay *DelayActionConfig `json:"delay,omitempty"`
}

// ReactionBlock is a complete trigger → action pipeline unit
type ReactionBlock struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Enabled bool         `json:"enabled"`
	Trigger TriggerBlock `json:"trigger"`
	Actions []Action     `json:"actions"`
}

// ExecutionLog records a single action execution
type ExecutionLog struct {
	BlockID      string    `json:"blockId"`
	BlockName    string    `json:"blockName"`
	ActionIndex  int       `json:"actionIndex"`
	ActionType   string    `json:"actionType"`
	Timestamp    time.Time `json:"timestamp"`
	TriggerTopic string    `json:"triggerTopic"`
	StatusCode   int       `json:"statusCode"`
	DurationMs   int64     `json:"durationMs"`
	Error        string    `json:"error,omitempty"`
}
