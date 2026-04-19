package sample

import "fmt"

type UserService struct{}

func (s *UserService) GetUser(id int) string {
	return fmt.Sprintf("user-%d", id)
}

func HandleRequest() {
	svc := &UserService{}
	svc.GetUser(1)
}
