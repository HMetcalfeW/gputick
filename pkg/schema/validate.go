package schema

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

var (
	ErrInferredUndefined = errors.New("inferred availability signals are not defined in methodology " + MethodologyVersion)
	ErrAvailabilityValue = errors.New("availability_value does not match availability_signal encoding")
)

// Validate checks the invariants from docs/SCHEMA.md section 3: required
// fields, enumeration membership, the availability encoding and the staleness
// rules. It returns every violation joined, not just the first.
func (o Observation) Validate() error {
	var errs []error
	if o.ObservedAt.IsZero() {
		errs = append(errs, errors.New("observed_at is zero"))
	} else {
		errs = appendErr(errs, CheckUTC("observed_at", o.ObservedAt))
	}
	for name, v := range map[string]string{
		"run_id": o.RunID, "source": o.Source, "provider": o.Provider,
		"gpu_slug": o.GPUSlug, "gpu_vendor": o.GPUVendor, "gpu_model": o.GPUModel,
		"tier": o.Tier, "currency_original": o.CurrencyOriginal,
		"methodology_version": o.MethodologyVersion,
	} {
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is empty", name))
		}
	}
	if o.RunID != "" {
		if _, err := RunIDTime(o.RunID); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, o.validateEnums()...)
	errs = append(errs, o.validateNumbers()...)
	if err := o.Availability().Validate(); err != nil {
		errs = append(errs, err)
	}
	if o.Stale && o.StaleSince == nil {
		errs = append(errs, errors.New("stale_since is required when stale is true"))
	}
	if !o.Stale && o.StaleSince != nil {
		errs = append(errs, errors.New("stale_since must be null when stale is false"))
	}
	if o.StaleSince != nil {
		errs = appendErr(errs, CheckUTC("stale_since", *o.StaleSince))
	}
	return errors.Join(errs...)
}

// CheckUTC reports an error when t has a non-zero UTC offset. The contract
// requires timestamps to serialize with a Z suffix, which encoding/json does
// for any zero-offset time, so time.Now() on a UTC host passes and a local
// time elsewhere does not.
func CheckUTC(name string, t time.Time) error {
	if _, offset := t.Zone(); offset != 0 {
		return fmt.Errorf("%s must be UTC, got offset %+d seconds; call .UTC() before writing", name, offset)
	}
	return nil
}

func appendErr(errs []error, err error) []error {
	if err != nil {
		return append(errs, err)
	}
	return errs
}

func (o Observation) validateNumbers() []error {
	var errs []error
	if o.GPUMemoryGB <= 0 {
		errs = append(errs, errors.New("gpu_memory_gb must be positive"))
	}
	if o.NodeGPUCount <= 0 {
		errs = append(errs, errors.New("node_gpu_count must be positive"))
	}
	if o.PriceUSDPerGPUHour <= 0 {
		errs = append(errs, errors.New("price_usd_per_gpu_hour must be positive"))
	}
	if o.PriceOriginal <= 0 {
		errs = append(errs, errors.New("price_original must be positive"))
	}
	if o.MinCommitmentHours < 0 {
		errs = append(errs, errors.New("min_commitment_hours must not be negative"))
	}
	if o.CurrencyOriginal == "USD" && o.FXRateToUSD != nil {
		errs = append(errs, errors.New("fx_rate_to_usd must be null when currency_original is USD"))
	}
	return errs
}

func (o Observation) validateEnums() []error {
	var errs []error
	if !o.FormFactor.Valid() {
		errs = append(errs, fmt.Errorf("form_factor %q is not a FormFactor", o.FormFactor))
	}
	if !o.Interconnect.Valid() {
		errs = append(errs, fmt.Errorf("interconnect %q is not an Interconnect", o.Interconnect))
	}
	if !o.RegionBucket.Valid() {
		errs = append(errs, fmt.Errorf("region_bucket %q is not a RegionBucket", o.RegionBucket))
	}
	if !o.PriceType.Valid() {
		errs = append(errs, fmt.Errorf("price_type %q is not a PriceType", o.PriceType))
	}
	if !o.PriceBasis.Valid() {
		errs = append(errs, fmt.Errorf("price_basis %q is not a PriceBasis", o.PriceBasis))
	}
	if !o.PriceStat.Valid() {
		errs = append(errs, fmt.Errorf("price_stat %q is not a PriceStat", o.PriceStat))
	}
	return errs
}

// Validate checks that Value is encoded correctly for Signal and that
// Available equals what DeriveAvailable produces from them.
func (a Availability) Validate() error {
	if !a.Signal.Valid() {
		return fmt.Errorf("availability_signal %q is not an AvailabilitySignal", a.Signal)
	}
	want, err := a.DeriveAvailable()
	if err != nil {
		return err
	}
	if !equalBoolPtr(want, a.Available) {
		return fmt.Errorf("available %s does not match derivation %s for signal %s",
			fmtBoolPtr(a.Available), fmtBoolPtr(want), a.Signal)
	}
	return nil
}

// DeriveAvailable applies the per-signal derivation table from
// docs/METHODOLOGY.md section 4. A nil result means the signal does not
// justify a yes or no. The error reports a Value that violates the encoding.
func (a Availability) DeriveAvailable() (*bool, error) {
	switch a.Signal {
	case SignalNone:
		if a.Value != nil {
			return nil, fmt.Errorf("%w: value must be null for signal none", ErrAvailabilityValue)
		}
		return nil, nil
	case SignalCapacityFlag:
		return deriveCapacityFlag(a.Value)
	case SignalStockLevel:
		if a.Value == nil || *a.Value == "" {
			return nil, fmt.Errorf("%w: stock_level requires a value", ErrAvailabilityValue)
		}
		return boolPtr(*a.Value != "none"), nil
	case SignalOfferCount:
		n, err := parseUint(a.Value)
		if err != nil {
			return nil, fmt.Errorf("%w: offer_count: %w", ErrAvailabilityValue, err)
		}
		return boolPtr(n > 0), nil
	case SignalPlacementScore:
		n, err := parseUint(a.Value)
		if err != nil || n < 1 || n > 10 {
			return nil, fmt.Errorf("%w: placement_score must be an integer from 1 to 10", ErrAvailabilityValue)
		}
		return nil, nil
	case SignalInferred:
		return nil, ErrInferredUndefined
	default:
		return nil, fmt.Errorf("availability_signal %q is not an AvailabilitySignal", a.Signal)
	}
}

func deriveCapacityFlag(v *string) (*bool, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: capacity_flag requires a value", ErrAvailabilityValue)
	}
	switch *v {
	case "true":
		return boolPtr(true), nil
	case "false":
		return boolPtr(false), nil
	default:
		return nil, fmt.Errorf("%w: capacity_flag must be \"true\" or \"false\", got %q", ErrAvailabilityValue, *v)
	}
}

func parseUint(v *string) (uint64, error) {
	if v == nil {
		return 0, errors.New("value is null")
	}
	return strconv.ParseUint(*v, 10, 64)
}

func boolPtr(b bool) *bool { return &b }

func equalBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func fmtBoolPtr(b *bool) string {
	if b == nil {
		return "null"
	}
	return strconv.FormatBool(*b)
}
