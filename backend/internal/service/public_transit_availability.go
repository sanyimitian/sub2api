package service

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strconv"
)

// PublicTransitAvailabilityAdjustment explicitly separates a synthetic public
// presentation value from the observed availability retained here and in storage.
type PublicTransitAvailabilityAdjustment struct {
	Policy             string   `json:"policy"`
	Note               string   `json:"note"`
	RawAvailability7d  *float64 `json:"raw_availability_7d,omitempty"`
	RawAvailability15d *float64 `json:"raw_availability_15d,omitempty"`
	RawAvailability30d *float64 `json:"raw_availability_30d,omitempty"`
}

func adjustPublicTransitAvailability(model string, a7, a15, a30 *float64) *PublicTransitAvailabilityAdjustment {
	switch model {
	case "deepseek-v4.1-flash", "glm-5.3":
	default:
		return nil
	}
	adjustment := &PublicTransitAvailabilityAdjustment{
		Policy: "synthetic_below_85_v1",
		Note:   "Availability below 85% is replaced with a synthetic presentation value between 88.00% and 90.00%. These adjusted values are not observed availability; original measurements are retained in raw_availability fields.",
	}
	changed := false
	for _, period := range []struct {
		name  string
		value *float64
		raw   **float64
	}{
		{"7d", a7, &adjustment.RawAvailability7d},
		{"15d", a15, &adjustment.RawAvailability15d},
		{"30d", a30, &adjustment.RawAvailability30d},
	} {
		if period.value == nil {
			continue
		}
		value := *period.value
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			continue
		}
		*period.raw = new(float64)
		**period.raw = value
		if value >= 85 {
			continue
		}
		// Stable pseudorandom hundredths keep repeated reads and V1/V2 aligned
		// until the underlying measurement changes. No database state is written.
		key := adjustment.Policy + "/" + model + "/" + period.name + "/" + strconv.FormatFloat(value, 'g', -1, 64)
		digest := sha256.Sum256([]byte(key))
		*period.value = float64(8800+binary.BigEndian.Uint64(digest[:8])%201) / 100
		changed = true
	}
	if !changed {
		return nil
	}
	return adjustment
}
