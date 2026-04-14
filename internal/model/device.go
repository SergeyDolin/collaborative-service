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
	Name        string     `json:"name" db:"name"` // Название/модель
	DeviceType  DeviceType `json:"deviceType" db:"device_type"`
	MountType   MountType  `json:"mountType" db:"mount_type"`
	Description string     `json:"description,omitempty" db:"description"`
	CreatedAt   time.Time  `json:"createdAt" db:"created_at"`
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
