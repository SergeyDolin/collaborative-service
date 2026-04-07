package model

// ProcessingMethod method of adjustment
type ProcessingMethod string

const (
	MethodSingle   ProcessingMethod = "single"
	MethodRelative ProcessingMethod = "relative"
	MethodPPP      ProcessingMethod = "ppp"
)

// ProcessingMode mode of adjustment
type ProcessingMode string

const (
	ModeKinematic ProcessingMode = "kinematic"
	ModeStatic    ProcessingMode = "static"
)

// FrequencyBand frequencies
type FrequencyBand string

const (
	FreqL1   FrequencyBand = "l1"
	FreqL2   FrequencyBand = "l2"
	FreqL5   FrequencyBand = "l5"
	FreqL1L2 FrequencyBand = "l1+l2"
	FreqAll  FrequencyBand = "l1+l2+l5"
)

// IonoModel model of ionosphere
type IonoModel string

const (
	IonoOff      IonoModel = "off"
	IonoBRDC     IonoModel = "brdc"
	IonoSBAS     IonoModel = "sbas"
	IonoDualFreq IonoModel = "dual-freq"
	IonoEstSTEC  IonoModel = "est-stec"
)

// TropModel model of troposphere
type TropModel string

const (
	TropOff    TropModel = "off"
	TropSAAS   TropModel = "saas"
	TropEstZTD TropModel = "est-ztd"
)

// ARMode Ambiguity Resolution
type ARMode string

const (
	AROff        ARMode = "off"
	ARContinuous ARMode = "continuous"
	ARInstant    ARMode = "instantaneous"
	ARFixAndHold ARMode = "fix-and-hold"
)

// UserProcessingConfig configuration
type UserProcessingConfig struct {
	Method      ProcessingMethod `json:"method"`
	Mode        ProcessingMode   `json:"mode,omitempty"`
	BaseStation string           `json:"baseStation,omitempty"`

	// Automatic parameters
	Frequency       FrequencyBand `json:"frequency"`
	ElevationMask   float64       `json:"elevationMask"`
	IonoModel       IonoModel     `json:"ionoModel"`
	TropModel       TropModel     `json:"tropModel"`
	ARMode          ARMode        `json:"arMode"`
	TideCorr        bool          `json:"tideCorr"`
	SatelliteSystem int           `json:"satelliteSystem"`

	// Flags for additional files
	UsePreciseEphemeris bool `json:"usePreciseEphemeris"`
	UsePreciseClock     bool `json:"usePreciseClock"`
	UseDCB              bool `json:"useDcb"`
	UseERP              bool `json:"useErp"`
	UseOSB              bool `json:"useOsb"`
}

// DefaultConfig return config
func DefaultConfig(method ProcessingMethod) UserProcessingConfig {
	switch method {
	case MethodRelative:
		return UserProcessingConfig{
			Method:              method,
			Frequency:           FreqAll,
			ElevationMask:       10.0,
			IonoModel:           IonoDualFreq,
			TropModel:           TropSAAS,
			ARMode:              ARFixAndHold,
			TideCorr:            false,
			SatelliteSystem:     61,
			UsePreciseEphemeris: false,
			UsePreciseClock:     false,
			UseDCB:              false,
			UseERP:              false,
			UseOSB:              false,
		}
	case MethodPPP:
		return UserProcessingConfig{
			Method:              method,
			Frequency:           FreqAll,
			ElevationMask:       10.0,
			IonoModel:           IonoDualFreq,
			TropModel:           TropEstZTD,
			ARMode:              ARContinuous,
			TideCorr:            true,
			SatelliteSystem:     61,
			UsePreciseEphemeris: true,
			UsePreciseClock:     true,
			UseDCB:              true,
			UseERP:              true,
			UseOSB:              true,
		}
	default: // MethodAbsolute
		return UserProcessingConfig{
			Method:              method,
			Frequency:           FreqL1,
			ElevationMask:       10.0,
			IonoModel:           IonoBRDC,
			TropModel:           TropSAAS,
			ARMode:              AROff,
			TideCorr:            false,
			SatelliteSystem:     1,
			UsePreciseEphemeris: false,
			UsePreciseClock:     false,
			UseDCB:              false,
			UseERP:              false,
		}
	}
}
