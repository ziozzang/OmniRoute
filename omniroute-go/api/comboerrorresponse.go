package api

import "encoding/json"

// comboerrorresponse.go — Combo API error helper.
// Port of src/lib/api/comboErrorResponse.ts
//
// Pure functions — no race concerns.

// ComboErrorCode is a stable machine-readable combo error token.
type ComboErrorCode string

const (
	ComboInvalidJSON   ComboErrorCode = "COMBO_001"
	ComboSchemaFailure ComboErrorCode = "COMBO_002"
	ComboTierInvalid   ComboErrorCode = "COMBO_003"
	ComboNameCollision ComboErrorCode = "COMBO_004"
	ComboDAGCycle      ComboErrorCode = "COMBO_005"
	ComboQuotaManaged  ComboErrorCode = "COMBO_006"
	ComboNotFound      ComboErrorCode = "COMBO_007"
	ValidInvalidBody   ComboErrorCode = "VALID_001"
	ValidMissingField  ComboErrorCode = "VALID_002"
	InternalFallback   ComboErrorCode = "INTERNAL_001"
)

// ErrorDef is one entry in the error code registry.
type ErrorDef struct {
	Code       ComboErrorCode
	Message    string
	Category   string
	HTTPStatus int
}

// errorCodes mirrors ERROR_CODES from src/shared/constants/errorCodes.ts.
var errorCodes = map[ComboErrorCode]ErrorDef{
	ComboInvalidJSON:   {ComboInvalidJSON, "Request body is not valid JSON", "validation", 400},
	ComboSchemaFailure: {ComboSchemaFailure, "Request failed schema validation", "validation", 400},
	ComboTierInvalid:   {ComboTierInvalid, "Composite tier configuration is invalid", "validation", 400},
	ComboNameCollision: {ComboNameCollision, "A combo with this name already exists", "conflict", 409},
	ComboDAGCycle:      {ComboDAGCycle, "Combo graph contains a cycle or exceeds max depth", "validation", 400},
	ComboQuotaManaged:  {ComboQuotaManaged, "This combo is managed by Quota Share", "conflict", 409},
	ComboNotFound:      {ComboNotFound, "Combo not found", "not_found", 404},
	ValidInvalidBody:   {ValidInvalidBody, "Invalid request body", "validation", 400},
	ValidMissingField:  {ValidMissingField, "Missing required field", "validation", 400},
	InternalFallback:   {InternalFallback, "Internal server error", "internal", 500},
}

// ComboErrorBody is the combo error JSON envelope.
type ComboErrorBody struct {
	Error struct {
		Code      ComboErrorCode `json:"code"`
		Message   string         `json:"message"`
		Category  string         `json:"category"`
		Details   any            `json:"details,omitempty"`
		RequestID string         `json:"requestId,omitempty"`
	} `json:"error"`
}

// BuildComboErrorBody builds the combo error envelope for a code.
func BuildComboErrorBody(code ComboErrorCode, details any, requestID string) ComboErrorBody {
	def, ok := errorCodes[code]
	if !ok {
		def = errorCodes[InternalFallback]
	}
	var out ComboErrorBody
	out.Error.Code = def.Code
	out.Error.Message = def.Message
	out.Error.Category = def.Category
	out.Error.Details = details
	out.Error.RequestID = requestID
	return out
}

// ComboErrorResponse builds the combo error JSON body and HTTP status.
func ComboErrorResponse(code ComboErrorCode, statusOverride int, details any, requestID string) (body []byte, status int) {
	def, ok := errorCodes[code]
	if !ok {
		def = errorCodes[InternalFallback]
	}
	status = def.HTTPStatus
	if statusOverride > 0 {
		status = statusOverride
	}
	out := BuildComboErrorBody(code, details, requestID)
	data, _ := json.Marshal(out)
	return data, status
}
