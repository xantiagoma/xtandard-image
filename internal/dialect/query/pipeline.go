package query

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
)

// maxPipelineOperations is imaginary's limit (image.go Pipeline).
const maxPipelineOperations = 10

// pipelineOperation is one element of imaginary's operations JSON
// (options.go PipelineOperation).
type pipelineOperation struct {
	Name          string         `json:"operation"`
	IgnoreFailure bool           `json:"ignore_failure"`
	Params        map[string]any `json:"params"`
}

// opPipeline chains one pipeline per operation. Output params (type,
// quality, ...) are request-wide, so a later operation overrides an
// earlier one. Top-level params other than operations are validated but
// not applied, as in imaginary.
func opPipeline(b *imgproxy.Builder, ip *iparams) error {
	ops, err := parseOperations(ip.operations)
	if err != nil {
		return err
	}
	for i, op := range ops {
		fn, ok := operation(op.Name, true)
		if !ok {
			return dialect.InvalidArgument("operations.operation", op.Name, "supported imaginary operation")
		}
		if i > 0 {
			if err := b.NewPipeline(); err != nil {
				return err
			}
		}
		opParams := newIParams()
		// Sorted for a deterministic first error.
		for _, key := range slices.Sorted(maps.Keys(op.Params)) {
			if _, err := opParams.set(key, op.Params[key]); err != nil {
				return err
			}
		}
		if op.IgnoreFailure {
			b.Ignore("ignore_failure", reasonNotImplemented)
		}
		if err := fn(b, opParams); err != nil {
			return err
		}
	}
	return nil
}

// parseOperations decodes the operations JSON with unknown fields
// rejected (params.go parseJSONOperations).
func parseOperations(s string) ([]pipelineOperation, error) {
	if s == "" {
		return nil, missing("operations", expRequired)
	}
	var ops []pipelineOperation
	d := json.NewDecoder(strings.NewReader(s))
	d.DisallowUnknownFields()
	if err := d.Decode(&ops); err != nil || d.More() {
		return nil, dialect.InvalidArgument("operations", s, "JSON array of operations")
	}
	if len(ops) == 0 {
		return nil, missing("operations", expRequired)
	}
	if len(ops) > maxPipelineOperations {
		return nil, dialect.InvalidArgument("operations", s, "at most 10 operations")
	}
	return ops, nil
}
