package interfaces

type UserStorage interface {
	CreateUser(login, password string) error
}

type Storage interface {
	UserStorage
	Close() error
}
