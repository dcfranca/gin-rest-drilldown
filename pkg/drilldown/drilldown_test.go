package drilldown

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/docker/go-connections/nat"
	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type Author struct {
	ID        uint64  `json:"id"`
	Name      *string `json:"name" gorm:"index:idx_name,unique" binding:"required"`
	Books     []Book
	UpdatedAt uint8 `json:"updated_at,omitempty"`
	CreatedAt uint8 `json:"created_at,omitempty"`
}

type Book struct {
	ID        uint64  `json:"id"`
	Title     *string `json:"title,omitempty" gorm:"not null"`
	AuthorID  uint64  `json:"author_id,omitempty" binding:"required"`
	Genre     *string `json:"genre,omitempty"`
	Pages     *int    `json:"pages,omitempty"`
	Slug      *string `json:"slug,omitempty"`
	UpdatedAt uint64  `json:"updated_at,omitempty"`
	CreatedAt uint64  `json:"created_at,omitempty"`
}

type ItemStringID struct {
	ID   *string `json:"id" gorm:"primarykey"`
	Name *string `json:"name" binding:"required"`
}

func init() {
	testing.Init()
	flag.Parse()
}

func ConnectDatabase(connectionString string) {
	database, err := gorm.Open(mysql.Open(connectionString))

	if err != nil {
		panic("Failed to connect to database!")
	}

	DB = database
}

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

func initializeTestDatabase(t *testing.T) (*gin.Engine, context.Context, *sql.DB, testcontainers.Container) {
	ctx := context.Background()

	// container and database
	container, db, connectionString, err := CreateTestContainer(ctx, "testdb")
	if err != nil {
		t.Fatal(err)
	}

	ConnectDatabase(connectionString)
	router := SetupRouter()
	return router, ctx, db, container
}

func TestHealthCheck(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	// Test health check
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/healthcheck", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Ok", w.Body.String())
}

func TestFullFlow(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&Book{})
	DB.AutoMigrate(&Author{})
	RegisterModel(router, Book{}, "books", &ApiConfig{})

	author := Author{Name: stringPtr("Chuck Palahniuk")}
	DB.Create(&author)

	path := "/books"

	// Test get users empty
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ := response["data"].([]interface{})
	assert.Len(t, dataItems, 0)

	// Test insert new record
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, path, bytes.NewBufferString(fmt.Sprintf(`{"title":"Fight Club", "author_id": %v}`, author.ID)))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	data, _ := response["data"].(map[string]interface{})
	title := data["title"]
	assert.Equal(t, title, "Fight Club")

	// Test get list of books
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 1)

	// Test get single book
	w = httptest.NewRecorder()
	singleUrl := fmt.Sprintf("/books/%v", data["id"])
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItem, _ := response["data"].(map[string]interface{})
	title = dataItem["title"]
	assert.Equal(t, title, "Fight Club")

	// Update record
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPut, singleUrl, bytes.NewBufferString(`{"author_id": 1, "pages": 279}`))
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Test get single record updated
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	data, _ = response["data"].(map[string]interface{})
	title = data["title"]
	pages := data["pages"]
	assert.Equal(t, "Fight Club", title)
	assert.Equal(t, float64(279), pages)

	// Test delete record
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Test get records empty again
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 0)
}

func TestDifferentLookupField(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&Book{})
	RegisterModel(router, Book{}, "books", &ApiConfig{LookupField: "Slug"})

	path := "/books"

	// Test get books empty
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ := response["data"].([]interface{})
	assert.Len(t, dataItems, 0)

	// Test insert new record
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"title":"Fight Club", "author_id": 1, "slug": "fight-club"}`))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	data, _ := response["data"].(map[string]interface{})
	title := data["title"]
	slug := data["slug"]
	assert.Equal(t, title, "Fight Club")
	assert.Equal(t, slug, "fight-club")

	// Test get list of books
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 1)

	// Test get single book
	w = httptest.NewRecorder()
	singleUrl := fmt.Sprintf("/books/%v", data["slug"])
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItem, _ := response["data"].(map[string]interface{})
	title = dataItem["title"]
	slug = dataItem["slug"]
	assert.Equal(t, title, "Fight Club")
	assert.Equal(t, slug, "fight-club")

	// Update record
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPut, singleUrl, bytes.NewBufferString(`{"author_id": 1, "pages": 279}`))
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Test get single record updated
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	data, _ = response["data"].(map[string]interface{})
	title = data["title"]
	pages := data["pages"]
	assert.Equal(t, "Fight Club", title)
	assert.Equal(t, float64(279), pages)

	// Test delete record
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Test get records empty again
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 0)
}

func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}

func TestQueries(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&Book{})
	DB.AutoMigrate(&Author{})
	RegisterModel(router, Book{}, "books", &ApiConfig{})

	chuckPalahniuk := Author{Name: stringPtr("Chuck Palahniuk")}
	richardGreene := Author{Name: stringPtr("Richard Greene")}
	robertHoward := Author{Name: stringPtr("Robert E Howard")}
	anthonyBurgess := Author{Name: stringPtr("Anthony Burgess")}
	isaacAsimov := Author{Name: stringPtr("Isaac Asimov")}

	DB.Create(&chuckPalahniuk)
	DB.Create(&richardGreene)
	DB.Create(&robertHoward)
	DB.Create(&anthonyBurgess)
	DB.Create(&isaacAsimov)

	books := []Book{
		{Title: stringPtr("Fight Club"), AuthorID: chuckPalahniuk.ID, Pages: intPtr(279)},
		{Title: stringPtr("Survivor"), AuthorID: chuckPalahniuk.ID, Pages: intPtr(353)},
		{Title: stringPtr("Haunted"), AuthorID: chuckPalahniuk.ID, Pages: intPtr(692), Genre: stringPtr("Horror")},
		{Title: stringPtr("Fight Story"), AuthorID: robertHoward.ID, Pages: intPtr(75)},
		{Title: stringPtr("American Horror Story"), AuthorID: richardGreene.ID, Pages: intPtr(225)},
		{Title: stringPtr("A Clockwork Orange"), AuthorID: anthonyBurgess.ID, Pages: intPtr(175)},
		{Title: stringPtr("Prelude to Foundation"), AuthorID: isaacAsimov.ID, Pages: intPtr(481), Genre: stringPtr("SciFi")},
		{Title: stringPtr("Nightfall"), AuthorID: isaacAsimov.ID, Pages: intPtr(501), Genre: stringPtr("SciFi")},
	}

	for _, b := range books {
		DB.Create(&b)
	}

	path := "/books"

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems := response["data"].([]interface{})
	assert.Len(t, dataItems, len(books))
	assert.Equal(t, float64(1), dataItems[0].(map[string]interface{})["id"])
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, float64(len(books)), dataItems[len(books)-1].(map[string]interface{})["id"])
	assert.Equal(t, "Nightfall", dataItems[len(books)-1].(map[string]interface{})["title"])

	// Test query with single field
	w = httptest.NewRecorder()
	url := fmt.Sprintf("%v?fields=pages", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, len(books))
	assert.Equal(t, float64(1), dataItems[0].(map[string]interface{})["id"])
	assert.Equal(t, float64(279), dataItems[0].(map[string]interface{})["pages"])
	_, ok := dataItems[0].(map[string]interface{})["title"]
	assert.False(t, ok)
	assert.Equal(t, float64(len(books)), dataItems[len(books)-1].(map[string]interface{})["id"])
	assert.Equal(t, float64(501), dataItems[len(books)-1].(map[string]interface{})["pages"])
	_, ok = dataItems[len(books)-1].(map[string]interface{})["title"]
	assert.False(t, ok)

	// Test query with multiple fields
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,pages", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, len(books))
	assert.Equal(t, float64(1), dataItems[0].(map[string]interface{})["id"])
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, float64(279), dataItems[0].(map[string]interface{})["pages"])
	_, ok = dataItems[0].(map[string]interface{})["author.name"]
	assert.False(t, ok)
	assert.Equal(t, float64(len(books)), dataItems[len(books)-1].(map[string]interface{})["id"])
	assert.Equal(t, "Nightfall", dataItems[len(books)-1].(map[string]interface{})["title"])
	assert.Equal(t, float64(501), dataItems[len(books)-1].(map[string]interface{})["pages"])
	_, ok = dataItems[len(books)-1].(map[string]interface{})["author.name"]
	assert.False(t, ok)

	// Test query with invalid field
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,publisher", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 0)

	errors := response["errors"]
	assert.Len(t, errors, 1)
	assert.Equal(t, "Invalid field on the fields selector: publisher", errors.([]interface{})[0])

	// Test query with multiple invalid fields
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,publisher,genri", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 0)

	errors = response["errors"]
	assert.Len(t, errors, 2)
	assert.Equal(t, "Invalid field on the fields selector: publisher", errors.([]interface{})[0])
	assert.Equal(t, "Invalid field on the fields selector: genri", errors.([]interface{})[1])

	// // Test query with a few fields and filtered by startswith
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&title__startswith=Fight", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})

	assert.Len(t, dataItems, 2)
	assert.Equal(t, float64(1), dataItems[0].(map[string]interface{})["id"])
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Chuck Palahniuk", dataItems[0].(map[string]interface{})["author.name"])

	assert.Equal(t, float64(4), dataItems[1].(map[string]interface{})["id"])
	assert.Equal(t, "Fight Story", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Robert E Howard", dataItems[1].(map[string]interface{})["author.name"])

	// // Test query with a few fields and filtered by endswith
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&title__endswith=Story", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 2)
	assert.Equal(t, float64(4), dataItems[0].(map[string]interface{})["id"])
	assert.Equal(t, "Fight Story", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Robert E Howard", dataItems[0].(map[string]interface{})["author.name"])

	assert.Equal(t, float64(5), dataItems[1].(map[string]interface{})["id"])
	assert.Equal(t, "American Horror Story", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Richard Greene", dataItems[1].(map[string]interface{})["author.name"])

	// Test query with a few fields and filtered by contains
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&title__contains=i", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 6)
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Survivor", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Fight Story", dataItems[2].(map[string]interface{})["title"])
	assert.Equal(t, "American Horror Story", dataItems[3].(map[string]interface{})["title"])
	assert.Equal(t, "Prelude to Foundation", dataItems[4].(map[string]interface{})["title"])
	assert.Equal(t, "Nightfall", dataItems[5].(map[string]interface{})["title"])

	// Test query with a few fields and filtered by equal
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	errors, _ = response["errors"].([]interface{})
	assert.Len(t, dataItems, 3)
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Survivor", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Haunted", dataItems[2].(map[string]interface{})["title"])

	// Test query with a few fields and filtered by multiple equal
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk&title=Haunted", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 1)
	assert.Equal(t, "Haunted", dataItems[0].(map[string]interface{})["title"])

	// Test query with a few fields and filtered by gt
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk&pages__gt=500", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 1)
	assert.Equal(t, "Haunted", dataItems[0].(map[string]interface{})["title"])

	// Test query with a few fields and filtered by gte
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk&pages__gte=353", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 2)
	assert.Equal(t, "Survivor", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Haunted", dataItems[1].(map[string]interface{})["title"])

	// Test query with a few fields and filtered by lt
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk&pages__lt=300", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 1)
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])

	// Test query with a few fields and filtered by lte
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk&pages__lte=353", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 2)
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Survivor", dataItems[1].(map[string]interface{})["title"])

	// // Test query with order by
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk&order=title", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 3)
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Haunted", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Survivor", dataItems[2].(map[string]interface{})["title"])

	// Test query with order by DESC
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&authors.name=Chuck Palahniuk&order=-title", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 3)
	assert.Equal(t, "Survivor", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Haunted", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Fight Club", dataItems[2].(map[string]interface{})["title"])

	// // Test query with order by 2 fields
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&order=author.name,title", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})

	assert.Len(t, dataItems, 8)
	assert.Equal(t, "Anthony Burgess", dataItems[0].(map[string]interface{})["author.name"])
	assert.Equal(t, "Fight Club", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Nightfall", dataItems[4].(map[string]interface{})["title"])
	assert.Equal(t, "Richard Greene", dataItems[6].(map[string]interface{})["author.name"])
	assert.Equal(t, "Robert E Howard", dataItems[7].(map[string]interface{})["author.name"])

	// Test query with order by 2 fields
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&order=author.name,-title", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 8)
	assert.Equal(t, "Anthony Burgess", dataItems[0].(map[string]interface{})["author.name"])
	assert.Equal(t, "Survivor", dataItems[1].(map[string]interface{})["title"])
	assert.Equal(t, "Prelude to Foundation", dataItems[4].(map[string]interface{})["title"])
	assert.Equal(t, "Richard Greene", dataItems[6].(map[string]interface{})["author.name"])
	assert.Equal(t, "Robert E Howard", dataItems[7].(map[string]interface{})["author.name"])

	// Test invalid field for order by
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&order=publisher", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 0)
	errors = response["errors"]
	assert.Len(t, errors, 1)
	assert.Equal(t, "Invalid field on the order by: publisher", errors.([]interface{})[0])

	// // Test invalid field for where clause
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&publisher=dc", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 0)
	errors = response["errors"]
	assert.Len(t, errors, 1)
	assert.Equal(t, "Invalid field on the condition: publisher", errors.([]interface{})[0])

	// Test limit
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&order=author.name&limit=2", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	fmt.Println("ERRORS: ", response["errors"])
	assert.Len(t, dataItems, 2)
	assert.Equal(t, "Anthony Burgess", dataItems[0].(map[string]interface{})["author.name"])
	assert.Equal(t, "Chuck Palahniuk", dataItems[1].(map[string]interface{})["author.name"])

	// Test offset
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&order=author.name&offset=4", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 4)
	assert.Equal(t, "Isaac Asimov", dataItems[0].(map[string]interface{})["author.name"])

	// Test limit & offset
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&order=author.name&offset=2&limit=2", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 2)
	assert.Equal(t, "Chuck Palahniuk", dataItems[0].(map[string]interface{})["author.name"])

	// Test SQL Injection
	w = httptest.NewRecorder()
	url = fmt.Sprintf("%v?fields=title,authors.name&order=author.name;DROP TABLE books", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ = response["data"].([]interface{})
	assert.Len(t, dataItems, 8)

	// TODO: Improve standard way to get table name
	// TODO: Why integers are float?
}

func TestGetItem(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&Book{})
	DB.AutoMigrate(&Author{})
	RegisterModel(router, Book{}, "books", &ApiConfig{})
	RegisterModel(router, Author{}, "authors", nil)

	author := Author{Name: stringPtr("Chuck Palahniuk")}
	DB.Create(&author)

	book := Book{Title: stringPtr("Fight Club"), AuthorID: author.ID, Pages: intPtr(279)}
	DB.Create(&book)

	// Test Get item success
	w := httptest.NewRecorder()
	singleUrl := fmt.Sprintf("/books/%v", book.ID)
	req, _ := http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	dataItem, _ := response["data"].(map[string]interface{})
	assert.Equal(t, float64(book.ID), dataItem["id"])
	assert.Equal(t, "Fight Club", dataItem["title"])
	assert.Equal(t, float64(author.ID), dataItem["author_id"])

	// Test Get item from other table success
	w = httptest.NewRecorder()
	singleUrl = fmt.Sprintf("/authors/%v", author.ID)
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItem, _ = response["data"].(map[string]interface{})
	assert.Equal(t, float64(author.ID), dataItem["id"])
	assert.Equal(t, "Chuck Palahniuk", dataItem["name"])

	// Test Get item non existent
	w = httptest.NewRecorder()
	singleUrl = "/books/999"
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Test Get item invalid string id
	w = httptest.NewRecorder()
	singleUrl = "/books/invalid-id"
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetItemStringID(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&ItemStringID{})
	RegisterModel(router, ItemStringID{}, "items", &ApiConfig{})

	item := ItemStringID{ID: stringPtr("myid1"), Name: stringPtr("Test 123")}
	DB.Create(&item)

	// Test Get item success
	w := httptest.NewRecorder()
	singleUrl := fmt.Sprintf("/items/%v", *item.ID)
	req, _ := http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	dataItem, _ := response["data"].(map[string]interface{})
	assert.Equal(t, *item.ID, dataItem["id"])
	assert.Equal(t, "Test 123", dataItem["name"])
}

func TestInserts(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&Book{})
	DB.AutoMigrate(&Author{})
	RegisterModel(router, Book{}, "books", nil)
	RegisterModel(router, Author{}, "authors", nil)

	var response map[string]interface{}
	path := "/books"

	// Test insert new record without author
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"title":"Fight Club"}`))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "AuthorID : failed on tag validation: required", response["errors"].([]interface{})[0])

	// Test insert dependence
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/authors", bytes.NewBufferString(`{"name":"Chuck Palahniuk"}`))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]interface{})
	assert.Equal(t, float64(1), data["id"])

	// Test insert with not null field empty
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, path, bytes.NewBufferString(fmt.Sprintf(`{"author_id": %v, "title": null}`, data["id"])))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	json.Unmarshal(w.Body.Bytes(), &response)
	errors := response["errors"].([]interface{})
	assert.Equal(t, "Error 1048: Column 'title' cannot be null", errors[0])

	// Test insert with all required and dependence
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, path, bytes.NewBufferString(fmt.Sprintf(`{"author_id": %v, "title": "Fight Club"}`, data["id"])))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	json.Unmarshal(w.Body.Bytes(), &response)
	data = response["data"].(map[string]interface{})
	assert.Equal(t, "Fight Club", data["title"])

	// Test query inserted with dependence
	w = httptest.NewRecorder()
	url := fmt.Sprintf("%v?fields=title,authors.name", path)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	dataItems, _ := response["data"].([]interface{})
	assert.Len(t, dataItems, 1)
	assert.Equal(t, float64(1), dataItems[0].(map[string]interface{})["id"])
	assert.Equal(t, "Fight Club", dataItems[0].(map[string]interface{})["title"])
	assert.Equal(t, "Chuck Palahniuk", dataItems[0].(map[string]interface{})["author.name"])

	// Test insert duplicated unique key
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/authors", bytes.NewBufferString(`{"name":"Chuck Palahniuk"}`))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Error 1062: Duplicate entry 'Chuck Palahniuk' for key 'authors.idx_name'", response["errors"].([]interface{})[0])
}

func TestUpdates(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&Author{})
	DB.AutoMigrate(&Book{})
	RegisterModel(router, Book{}, "books", nil)

	author := Author{Name: stringPtr("Chuck Palahniuk")}
	DB.Create(&author)

	book := Book{Title: stringPtr("Fight Club"), AuthorID: author.ID, Pages: intPtr(279)}
	DB.Create(&book)

	var response map[string]interface{}

	// Test trying to update with non existent record
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/books/999", bytes.NewBufferString(`{"title":"Invalid"}`))
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Test Update successful
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPut, fmt.Sprintf("/books/%v", book.ID), bytes.NewBufferString(`{"genre": "drama"}`))
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	w = httptest.NewRecorder()
	url := fmt.Sprintf("/books/%v", book.ID)
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"]
	assert.Equal(t, float64(1), data.(map[string]interface{})["id"])
	assert.Equal(t, "Fight Club", data.(map[string]interface{})["title"])
	assert.Equal(t, "drama", data.(map[string]interface{})["genre"])
	assert.Equal(t, float64(279), data.(map[string]interface{})["pages"])
}

func TestDeletes(t *testing.T) {
	router, ctx, db, container := initializeTestDatabase(t)
	defer db.Close()
	defer container.Terminate(ctx)

	DB.AutoMigrate(&Book{})
	DB.AutoMigrate(&Author{})
	RegisterModel(router, Book{}, "books", nil)

	author := Author{Name: stringPtr("Chuck Palahniuk")}
	DB.Create(&author)

	book := Book{Title: stringPtr("Fight Club"), AuthorID: author.ID, Pages: intPtr(279)}
	DB.Create(&book)

	// Test Delete item success
	w := httptest.NewRecorder()
	singleUrl := fmt.Sprintf("/books/%v", book.ID)
	req, _ := http.NewRequest(http.MethodDelete, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Test Delete item non existent
	w = httptest.NewRecorder()
	singleUrl = "/books/999"
	req, _ = http.NewRequest(http.MethodDelete, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Test Delete item invalid string id
	w = httptest.NewRecorder()
	singleUrl = "/books/invalid-id"
	req, _ = http.NewRequest(http.MethodDelete, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Test Get item after deletion
	w = httptest.NewRecorder()
	singleUrl = fmt.Sprintf("/books/%v", book.ID)
	req, _ = http.NewRequest(http.MethodGet, singleUrl, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
