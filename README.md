# REST DrillDrown

This package is based on [Django Rest Framework DrillDrown](https://github.com/clearcare/django-rest-framework-drilldown) and it is on early stages of developments

The goal is to allow fast creation of REST APIs based on models, allowing advanced filtering, running on top of [Golang Gin Web framework](https://gin-gonic.com/)


# Examples

Assuming you have the following models:

```
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
	UpdatedAt uint64  `json:"updated_at,omitempty"`
	CreatedAt uint64  `json:"created_at,omitempty"`
}
```

You should be able to register a basic CRUD REST interface just adding:
```
	RegisterModel(router, Book{}, "books")
	RegisterModel(router, Author{}, "authors")
```
The first argument is the Gin router (*gin.Engine), the second argument is the instance of the model, and the last argument is the resource path on the URL

For example, this will create the following routes for the books:

`GET /books`
`GET    /books/:id`
`POST   /books`
`PUT    /books/:id`
`DELETE /books/:id`

The `GET /books` endpoint allows for more complex queries
Example of some of the possible queries:

Specify fields to retrieve using the `fields` parameter:
```
GET /books?fields=title,pages
```

Specify fields from join table using the syntax `tableName`.`field`:
```
GET /books?fields=title,authors.name
```


Specify condition using different operators:
```
GET /books?fields=title,authors.name&pages__gt=500
```

Possible operators:
* `__gt` -> Greater than
* `__gte` -> Greater than or equal
* `__lt` -> Less than
* `__lte` -> Less than or equal
* `__startswith` -> String starts with
* `__endswith` -> String ends with
* `__contains` -> String contains

Specify condition by referencing another table with the syntax `tableName`.`field`:
```
GET /books?fields=title,authors.name&authors.name=Chuck Palahniuk&pages__gt=500
```

Sort your results with `order` clause, add `-` prefix to sort in descending order
```
GET /books?fields=title,authors.name&order=author.name,-title
```

Paginate your results with `offset` and `limit`
```
GET /books?fields=title,authors.name&order=author.name&offset=21&limit=20
```
