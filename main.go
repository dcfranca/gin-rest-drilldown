package main

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/iancoleman/strcase"

	"github.com/gin-gonic/gin"
)

type Condition struct {
	Where string
 	Values []string
}

func isReservedField(f string) bool {

	if f == "fields" || f == "order" || f == "limit" || f == "offset"{
		return true
	}

	return false
}

func prepareCondition(query url.Values) Condition {
	where := ""
	values := []string{}
	for k, v := range query {
		if !isReservedField(k) {
			fieldAndOperation := strings.Split(k, "__")
			if len(where) > 0 {
				where = fmt.Sprintf("%v AND ", where)
			}

			if len(fieldAndOperation) == 1 {
				where = fmt.Sprintf("%v = ?", fieldAndOperation[0])
				values = append(values, v[0])
			} else if len(fieldAndOperation) == 2 {
				switch fieldAndOperation[1] {
					case "gt":
						where = fmt.Sprintf("%v > ?", fieldAndOperation[0])
					case "gt2":
						where = fmt.Sprintf("%v >= ?", fieldAndOperation[0])
					case "lt":
						where = fmt.Sprintf("%v < ?", fieldAndOperation[0])
					case "lte":
						where = fmt.Sprintf("%v <= ?", fieldAndOperation[0])
					case "like":
						where = fmt.Sprintf("%v LIKE ?", fieldAndOperation[0])
					default:
						where = fmt.Sprintf("%v = ?", fieldAndOperation[0])
				}
				if fieldAndOperation[1] == "like" {
					values = append(values, "%" + v[0] + "%")
				} else {
					values = append(values, v[0])
				}
			}
		}
	}

	return Condition{
		Where: where,
		Values: values,
	}
}

func prepareOrderBy(orderBy string) []string {
	preparedOrderBy := []string{}
	for _, f := range strings.Split(orderBy, ",") {
		if strings.HasPrefix(f, "-") {
			fs := f[1:]
			preparedOrderBy = append(preparedOrderBy, fmt.Sprintf("%v DESC", fs))
		} else {
			preparedOrderBy = append(preparedOrderBy, f)
		}
	}

	return preparedOrderBy
}



func registerModel[M any](r *gin.Engine, m M, path string) {

	// pathItem := fmt.Sprintf("%v/:id", path)
	r.GET(path, func(c *gin.Context) {
		qmap := c.Request.URL.Query()
		fmt.Println("query arguments: ", qmap)

		var items []M

		fp := qmap.Get("fields")
		fields := []string{}
		if fp != "" {
			fields = strings.Split(fp, ",")
			v := reflect.ValueOf(m)
			for _, f := range fields {
				// Check if field exists in the model
				if !v.FieldByName(strcase.ToCamel(f)).IsValid() {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("field not found: %v", f)})
					return
				}
			}
		}

		q := DB

		// SELECT
		if len(fields) > 0 {
			fields = append(fields, "id")
			q = q.Select(fields)
		}

		// TODO: Add drilldown relationship arguments to the query

		// WHERE
		cond := prepareCondition(qmap)
		if len(cond.Values) > 0 {
			fmt.Println("Condition: ", cond.Where)
			fmt.Println("Values: ", cond.Values)
			q = q.Where(cond.Where, cond.Values)
		}

		// ORDER
		orderBy := qmap.Get("order")
		if (orderBy != "") {
			ov := prepareOrderBy(orderBy)
			for _, o := range ov {
				fmt.Println("Order: ", o)
				q = q.Order(o)
			}
		}

		// OFFSET
		offset := qmap.Get("offset")
		if (offset != "") {
			q = q.Offset(offset)
		}

		// LIMIT
		limit := qmap.Get("limit")
		if (limit != "") {
			q = q.Limit(limit)
		}

		q.Find(&items)
		c.JSON(http.StatusOK, gin.H{"data": items})
	})

	// r.GET(pathItem, func(c *gin.Context) {
	// 	if err := DB.Where("id = ?", c.Param("id")).First(&items[0]).Error; err != nil {
	// 		c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
	// 		return
	// 	}

	// 	c.JSON(http.StatusOK, gin.H{"data": items[0]})
	// })

	r.POST(path, func(c *gin.Context) {
		// b, _ := c.GetRawData()
		// fmt.Println("DATA: ", string(b))
		var input M
		if err := c.BindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := DB.Create(&input).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusCreated, gin.H{"data": input})
		}
	})

	// 	c.JSON(http.StatusOK, gin.H{"data": item})
	// })

	// r.PUT(pathItem, func(c *gin.Context) {
	// 	c.JSON(http.StatusOK, gin.H{})
	// })

	// r.DELETE(pathItem, func(c *gin.Context) {
	// 	// Get model if exist
	// 	if err := models.DB.Where("id = ?", c.Param("id")).First(&items[0]).Error; err != nil {
	// 		c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
	// 		return
	// 	}

	// 	DB.Delete(&items[0])

	// 	c.JSON(http.StatusOK, gin.H{"data": true})
	// })
}

func setupRouter() *gin.Engine {
	r := gin.Default()

	// Healthcheck test
	r.GET("/healthcheck", func(c *gin.Context) {
		c.String(http.StatusOK, "Ok")
	})

	registerModel(r, User{}, "/users")

	return r
}

func main() {
	r := setupRouter()
	// Listen and Server in 0.0.0.0:8080
	ConnectDatabase("root:secret@/testdb?charset=utf8&parseTime=True&loc=Local")
	r.Run(":8080")
}
