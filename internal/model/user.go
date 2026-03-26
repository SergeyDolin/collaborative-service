package model

type User struct {
	Name       string      `json:"name"`
	SecondName string      `json:"secondname"`
	AuthData   AuthRequest `json:"authdata"`
}
