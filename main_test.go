package main

// Add datetime <CreatedAt/UpdatedAt>

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/docker/go-connections/nat"
	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func CreateTestContainer(ctx context.Context, dbname string) (testcontainers.Container, *sql.DB, string, error) {
	var env = map[string]string{
		"MYSQL_ROOT_PASSWORD": "password",
		"MYSQL_DATABASE":      dbname,
		"MYSQL_TCP_PORT":      "6605",
	}
	var port = "6605/tcp"

	log.Println("Requesting container...")

	req := testcontainers.ContainerRequest{
		Image:        "mysql:latest",
		ExposedPorts: []string{"6605/tcp"},
		Env:          env,
		WaitingFor:   wait.ForLog("port: 6605  MySQL Community Server - GPL"),
	}

	log.Println("Creating container...")
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return container, nil, "", fmt.Errorf("failed to start container: %s", err)
	}

	log.Println("Mapping port...")
	mappedPort, err := container.MappedPort(ctx, nat.Port(port))
	if err != nil {
		return container, nil, "", fmt.Errorf("failed to get container external port: %s", err)
	}

	log.Println("mysql container ready and running at port: ", mappedPort)

	host, _ := container.Host(ctx)
	p, _ := container.MappedPort(ctx, "6605/tcp")
	portI := p.Int()
	connectionString := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?tls=skip-verify",
		"root", "password", host, portI, dbname)

	log.Println("Connection String: ", connectionString)

	db, _ := sql.Open("mysql", connectionString)
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Panicf("error pinging db: %+v\n", err)
	}

	fmt.Println("Success created container")

	return container, db, connectionString, nil
}

func TestCreate(t *testing.T) {

	ctx := context.Background()

	// container and database
	container, db, connectionString, err := CreateTestContainer(ctx, "testdb")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer container.Terminate(ctx)

	ConnectDatabase(connectionString)

	router := setupRouter()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/healthcheck", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Ok", w.Body.String())

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/users", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"johndoe"}`))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var response map[string]interface{}

	json.Unmarshal(w.Body.Bytes(), &response)
	fmt.Println("RESPONSE: ", response)
	data, _ := response["data"].(map[string]interface{})
	username := data["username"]
	assert.Equal(t, username, "johndoe")
	fmt.Println(data)
}
