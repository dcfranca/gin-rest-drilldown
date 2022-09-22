package main

import (
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"gorm.io/driver/mysql"
	_ "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type User struct {
	ID        uint64  `json:"id"`
	Username  *string `json:"username,omitempty"`
	FirstName *string `json:"first_name,omitempty"`
	LastName  *string `json:"last_name,omitempty"`
	Age       *int    `json:"age,omitempty"`
}

var DB *gorm.DB

func ConnectDatabase(connectionString string) {
	database, err := gorm.Open(mysql.Open(connectionString))

	if err != nil {
		panic("Failed to connect to database!")
	}

	DB = database
}

func MigrateTables() {
	DB.AutoMigrate(&User{})
}
