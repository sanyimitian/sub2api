package service

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
)

type PublicTransitAvailabilityRange struct {
	Minimum float64
	Maximum float64
}

func DefaultPublicTransitAvailabilityRange() PublicTransitAvailabilityRange {
	return PublicTransitAvailabilityRange{Minimum: 88, Maximum: 90}
}

func (r PublicTransitAvailabilityRange) Validate() error {
	for _, value := range []float64{r.Minimum, r.Maximum} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 || math.Round(value*100)/100 != value {
			return fmt.Errorf("availability presentation bounds must be between 0 and 100 with at most two decimal places")
		}
	}
	if r.Minimum > r.Maximum {
		return fmt.Errorf("availability presentation minimum must not exceed maximum")
	}
	return nil
}

func parsePublicTransitAvailabilityRange(values map[string]string) (PublicTransitAvailabilityRange, error) {
	r := DefaultPublicTransitAvailabilityRange()
	for _, field := range []struct {
		key   string
		value *float64
	}{
		{SettingKeyPublicTransitAvailabilityAdjustmentMin, &r.Minimum},
		{SettingKeyPublicTransitAvailabilityAdjustmentMax, &r.Maximum},
	} {
		if raw, exists := values[field.key]; exists {
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return DefaultPublicTransitAvailabilityRange(), fmt.Errorf("invalid %s: %w", field.key, err)
			}
			*field.value = value
		}
	}
	if err := r.Validate(); err != nil {
		return DefaultPublicTransitAvailabilityRange(), err
	}
	return r, nil
}

// PublicTransitAvailabilityAdjustment explicitly separates a synthetic public
// presentation value from the observed availability retained here and in storage.
type PublicTransitAvailabilityAdjustment struct {
	Policy             string   `json:"policy"`
	Note               string   `json:"note"`
	DisplayMinimum     float64  `json:"display_minimum"`
	DisplayMaximum     float64  `json:"display_maximum"`
	RawAvailability7d  *float64 `json:"raw_availability_7d,omitempty"`
	RawAvailability15d *float64 `json:"raw_availability_15d,omitempty"`
	RawAvailability30d *float64 `json:"raw_availability_30d,omitempty"`
}

func adjustPublicTransitAvailability(model string, a7, a15, a30 *float64, displayRange PublicTransitAvailabilityRange) *PublicTransitAvailabilityAdjustment {
	if displayRange.Validate() != nil {
		return nil
	}
	switch model {
	case "deepseek-v4.1-flash", "glm-5.3":
	default:
		return nil
	}
	adjustment := &PublicTransitAvailabilityAdjustment{
		Policy:         "synthetic_below_85_v1",
		Note:           fmt.Sprintf("Availability below 85%% is replaced with a synthetic presentation value between %.2f%% and %.2f%%. These adjusted values are not observed availability; original measurements are retained in raw_availability fields.", displayRange.Minimum, displayRange.Maximum),
		DisplayMinimum: displayRange.Minimum,
		DisplayMaximum: displayRange.Maximum,
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
		// Preserve existing default-range values; custom bounds have their own seed.
		if displayRange != DefaultPublicTransitAvailabilityRange() {
			key += fmt.Sprintf("/%.2f/%.2f", displayRange.Minimum, displayRange.Maximum)
		}
		digest := sha256.Sum256([]byte(key))
		minimum := uint64(math.Round(displayRange.Minimum * 100))
		maximum := uint64(math.Round(displayRange.Maximum * 100))
		*period.value = float64(minimum+binary.BigEndian.Uint64(digest[:8])%(maximum-minimum+1)) / 100
		changed = true
	}
	if !changed {
		return nil
	}
	return adjustment
}
