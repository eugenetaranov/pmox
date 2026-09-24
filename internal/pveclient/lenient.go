package pveclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// flexInt decodes the loosely typed integers PVE emits: a JSON number,
// a quoted number ("1"), a bool (true → 1), an empty string or null
// (→ 0). Different PVE versions and endpoints disagree on how flags
// like template/active and ids like vmid are encoded, so struct fields
// that face the wire decode through this type.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch string(b) {
	case "null":
		return nil
	case "true":
		*f = 1
		return nil
	case "false":
		*f = 0
		return nil
	}
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt(n)
		return nil
	}
	// Tolerate float encodings of whole numbers (e.g. 1.0, 1e3).
	x, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		return fmt.Errorf("pveclient: cannot decode %s as integer", b)
	}
	*f = flexInt(x)
	return nil
}

// UnmarshalJSON decodes a Resource, accepting numbers, quoted numbers
// and bools for the integer fields.
func (r *Resource) UnmarshalJSON(b []byte) error {
	type plain Resource
	aux := struct {
		*plain
		VMID     flexInt `json:"vmid"`
		Uptime   flexInt `json:"uptime"`
		Template flexInt `json:"template"`
	}{plain: (*plain)(r), VMID: flexInt(r.VMID), Uptime: flexInt(r.Uptime), Template: flexInt(r.Template)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	r.VMID, r.Uptime, r.Template = int(aux.VMID), int64(aux.Uptime), int(aux.Template)
	return nil
}

// UnmarshalJSON decodes a Storage, accepting numbers, quoted numbers and
// bools for the active/enabled flags.
func (s *Storage) UnmarshalJSON(b []byte) error {
	type plain Storage
	aux := struct {
		*plain
		Active  flexInt `json:"active"`
		Enabled flexInt `json:"enabled"`
	}{plain: (*plain)(s), Active: flexInt(s.Active), Enabled: flexInt(s.Enabled)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	s.Active, s.Enabled = int(aux.Active), int(aux.Enabled)
	return nil
}

// UnmarshalJSON decodes a VMStatus, accepting numbers, quoted numbers
// and bools for the numeric fields.
func (v *VMStatus) UnmarshalJSON(b []byte) error {
	type plain VMStatus
	aux := struct {
		*plain
		VMID   flexInt `json:"vmid"`
		Uptime flexInt `json:"uptime"`
		CPUs   flexInt `json:"cpus"`
		Mem    flexInt `json:"mem"`
		MaxMem flexInt `json:"maxmem"`
	}{
		plain: (*plain)(v), VMID: flexInt(v.VMID), Uptime: flexInt(v.Uptime),
		CPUs: flexInt(v.CPUs), Mem: flexInt(v.Mem), MaxMem: flexInt(v.MaxMem),
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	v.VMID, v.Uptime, v.CPUs = int(aux.VMID), int64(aux.Uptime), int(aux.CPUs)
	v.Mem, v.MaxMem = int64(aux.Mem), int64(aux.MaxMem)
	return nil
}
