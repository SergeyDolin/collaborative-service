package model

import "time"

// DeviceType тип устройства
type DeviceType string

const (
	DeviceTypeGNSS       DeviceType = "gnss_receiver"
	DeviceTypeSmartphone DeviceType = "smartphone"
	DeviceTypeTablet     DeviceType = "tablet"
	DeviceTypeOther      DeviceType = "other"
)

// MountType тип установки устройства
type MountType string

const (
	MountTypeCar       MountType = "car"
	MountTypePermanent MountType = "permanent_station"
	MountTypeUAV       MountType = "uav"
	MountTypeRod       MountType = "rod"
)

// UserDevice устройство пользователя
type UserDevice struct {
	ID          int64      `json:"id" db:"id"`
	UserLogin   string     `json:"userLogin" db:"user_login"`
	Name        string     `json:"name" db:"name"`
	DeviceType  DeviceType `json:"deviceType" db:"device_type"`
	MountType   MountType  `json:"mountType" db:"mount_type"`
	Description string     `json:"description,omitempty" db:"description"`

	// Поля антенны ГНСС-приёмника
	AntennaName string  `json:"antennaName,omitempty" db:"antenna_name"`
	AntennaE    float64 `json:"antennaE" db:"antenna_e"`
	AntennaN    float64 `json:"antennaN" db:"antenna_n"`
	AntennaU    float64 `json:"antennaU" db:"antenna_u"`

	// Фазовый центр для мобильных устройств
	PhaseCenterMethod     string     `json:"phaseCenterMethod,omitempty" db:"phase_center_method"`
	PhaseCenterValidUntil *time.Time `json:"phaseCenterValidUntil,omitempty" db:"phase_center_valid_until"`

	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}

// DeviceTypeLabel человекочитаемое название типа
func (d DeviceType) Label() string {
	switch d {
	case DeviceTypeGNSS:
		return "ГНСС-приёмник"
	case DeviceTypeSmartphone:
		return "Смартфон"
	case DeviceTypeTablet:
		return "Планшет"
	default:
		return "Иное"
	}
}

// MountTypeLabel человекочитаемое название установки
func (m MountType) Label() string {
	switch m {
	case MountTypeCar:
		return "Автомобиль"
	case MountTypePermanent:
		return "Постояннодействующая станция"
	case MountTypeUAV:
		return "БПЛА"
	case MountTypeRod:
		return "Веха"
	default:
		return "Неизвестно"
	}
}
