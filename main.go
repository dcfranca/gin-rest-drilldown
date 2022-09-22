package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/iancoleman/strcase"
	"gorm.io/gorm"

	"github.com/gin-gonic/gin"
)

type Select struct {
	Fields []string
	Joins  []string
}

type Condition struct {
	Fields []string
	Where  string
	Joins  []string
	Values []string
}

type OrderBy struct {
	Field    string
	Modifier string
}

func isReservedField(f string) bool {

	if f == "fields" || f == "order" || f == "limit" || f == "offset" {
		return true
	}

	return false
}

func prepareSelectFields(fieldsP string, c chan Select) {
	preparedFields := []string{}
	joins := []string{}
	if fieldsP != "" {
		fields := strings.Split(fieldsP, ",")
		for _, f := range fields {
			if strings.Contains(f, ".") {
				// // Referenced table
				tableAndField := strings.Split(f, ".")
				singularTable := removePlural(tableAndField[0])
				join := fmt.Sprintf("left join `%[1]v` on `%[1]v`.id = %[2]v_id", tableAndField[0], singularTable)
				externalField := strings.Split(f, ".")[1]
				preparedFields = append(preparedFields, fmt.Sprintf("%v AS `%v.%v`", f, singularTable, externalField))
				joins = append(joins, join)
			} else {
				preparedFields = append(preparedFields, f)
			}
		}
	}

	c <- Select{
		Fields: preparedFields,
		Joins:  joins,
	}
}

func prepareCondition(resource string, query url.Values, c chan Condition) {
	where := ""
	fields := []string{}
	values := []string{}
	joins := []string{}
	for k, v := range query {
		if !isReservedField(k) {
			fieldAndOperation := strings.Split(k, "__")
			if len(where) > 0 {
				where = fmt.Sprintf("%v AND ", where)
			}

			fields = append(fields, fieldAndOperation[0])
			if len(fieldAndOperation) == 1 {
				where = fmt.Sprintf("%v%v = ?", where, fieldAndOperation[0])
				values = append(values, v[0])
			} else if len(fieldAndOperation) == 2 {
				switch fieldAndOperation[1] {
				case "gt":
					where = fmt.Sprintf("%v %v > ?", where, fieldAndOperation[0])
				case "gte":
					where = fmt.Sprintf("%v %v >= ?", where, fieldAndOperation[0])
				case "lt":
					where = fmt.Sprintf("%v %v < ?", where, fieldAndOperation[0])
				case "lte":
					where = fmt.Sprintf("%v %v <= ?", where, fieldAndOperation[0])
				case "startswith":
					where = fmt.Sprintf("%v %v LIKE ?", where, fieldAndOperation[0])
				case "endswith":
					where = fmt.Sprintf("%v %v LIKE ?", where, fieldAndOperation[0])
				case "contains":
					where = fmt.Sprintf("%v %v LIKE ?", where, fieldAndOperation[0])
				default:
					// It is referencing a different table
					where = fmt.Sprintf("%v %v = ?", where, fieldAndOperation[0])
					joins = append(joins, fmt.Sprintf("left join `%[1]v` on `%v[1]`.%v[2]_id = %[3]v.id",
						fieldAndOperation[0],
						fieldAndOperation[1],
						resource,
					))
				}
				if fieldAndOperation[1] == "startswith" {
					values = append(values, v[0]+"%")
				} else if fieldAndOperation[1] == "endswith" {
					values = append(values, "%"+v[0])
				} else if fieldAndOperation[1] == "contains" {
					values = append(values, "%"+v[0]+"%")
				} else {
					values = append(values, v[0])
				}
			}
		}
	}

	c <- Condition{
		Fields: fields,
		Where:  where,
		Joins:  joins,
		Values: values,
	}
}

func prepareOrderBy(orderBy string, c chan []OrderBy) {
	preparedOrderBy := []OrderBy{}
	for _, f := range strings.Split(orderBy, ",") {
		if strings.HasPrefix(f, "-") {
			fs := f[1:]
			preparedOrderBy = append(preparedOrderBy, OrderBy{
				Field:    fs,
				Modifier: "DESC",
			})
		} else if len(f) > 0 {
			preparedOrderBy = append(preparedOrderBy, OrderBy{
				Field: f,
			})
		}
	}

	c <- preparedOrderBy
}

func getIDParam(c *gin.Context) (uint64, error) {
	idS, ok := c.Params.Get("id")
	if !ok {
		return 0, fmt.Errorf("invalid ID")
	}

	id, err := strconv.Atoi(idS)
	if err != nil {
		return 0, fmt.Errorf("invalid ID %v", err)
	}

	return uint64(id), nil
}

func removePlural(s string) string {
	if strings.HasSuffix(s, "s") {
		return s[:len(s)-1]
	}

	return s
}

func IsTestRun() bool {
	return flag.Lookup("test.v").Value.(flag.Getter).Get().(bool)
}

func registerModel[M any](r *gin.Engine, m M, resource string) {

	path := "/" + resource
	pathItem := fmt.Sprintf("%v/:id", path)

	r.GET(path, func(c *gin.Context) {
		qmap := c.Request.URL.Query()
		var errors []string

		var q *gorm.DB
		if IsTestRun() {
			q = DB.Debug().Table(resource)
		} else {
			q = DB.Table(resource)
		}

		selectChan := make(chan Select)
		condChan := make(chan Condition)
		orderChan := make(chan []OrderBy)

		fp := qmap.Get("fields")
		go prepareSelectFields(fp, selectChan)
		go prepareCondition(resource, qmap, condChan)
		orderBy := qmap.Get("order")
		go prepareOrderBy(orderBy, orderChan)

		if len(errors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"errors": errors, "data": []M{}})
			return
		}

		for i := 0; i < 3; i++ {
			select {
			case sel := <-selectChan:
				// JOINS
				if len(sel.Joins) > 0 {
					for _, j := range sel.Joins {
						q = q.Joins(j)
					}
				}

				// SELECT
				if len(sel.Fields) > 0 {
					preparedFields := []string{}
					v := reflect.ValueOf(m)
					for _, f := range sel.Fields {
						if !strings.Contains(f, ".") && !v.FieldByName(strcase.ToCamel(f)).IsValid() {
							errors = append(errors, fmt.Sprintf("Invalid field on the fields selector: %v", f))
						} else {
							preparedFields = append(preparedFields, f)
						}
					}

					if len(errors) > 0 {
						c.JSON(http.StatusBadRequest, gin.H{"errors": errors, "data": []M{}})
						return
					}

					preparedFields = append(preparedFields, fmt.Sprintf("`%v`.id", resource))
					q = q.Select(preparedFields)
				}
			case cond := <-condChan:
				if len(cond.Joins) > 0 {
					for _, j := range cond.Joins {
						q = q.Joins(j)
					}
				}

				if len(cond.Values) > 0 {
					var vs []interface{}

					for _, f := range cond.Fields {
						v := reflect.ValueOf(m)
						// Check if field exists in the model
						if !strings.Contains(f, ".") && !v.FieldByName(strcase.ToCamel(f)).IsValid() {
							errors = append(errors, fmt.Sprintf("Invalid field on the condition: %v", f))
						}
					}

					if len(errors) > 0 {
						c.JSON(http.StatusBadRequest, gin.H{"errors": errors, "data": []M{}})
						return
					}

					for _, v := range cond.Values {
						vs = append(vs, v)
					}
					q = q.Where(cond.Where, vs...)
				}
			case ov := <-orderChan:
				for _, o := range ov {
					v := reflect.ValueOf(m)
					// Check if field exists in the model
					if !strings.Contains(o.Field, ".") && !v.FieldByName(strcase.ToCamel(o.Field)).IsValid() {
						errors = append(errors, fmt.Sprintf("Invalid field on the order by: %v", o.Field))
						continue
					}

					q = q.Order(fmt.Sprintf("`%v` %v", o.Field, o.Modifier))
				}

				if len(errors) > 0 {
					c.JSON(http.StatusBadRequest, gin.H{"errors": errors, "data": []M{}})
					return
				}
			}
		}

		// LIMIT
		limit := qmap.Get("limit")
		if limit != "" {
			limitI, err := strconv.Atoi(limit)
			if err != nil {
				errors = append(errors, fmt.Sprintf("Limit expects a number, received: %v", limit))
			} else {
				q = q.Limit(limitI)
			}
		}

		if len(errors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"errors": errors, "data": []M{}})
			return
		}

		// OFFSET
		offset := qmap.Get("offset")
		if offset != "" {
			offsetI, err := strconv.Atoi(offset)
			if err != nil {
				errors = append(errors, fmt.Sprintf("Offset expects a number, received: %v", offset))
			} else {
				if limit == "" {
					q = q.Limit(20) // Default pagination to 20
				}
				q = q.Offset(offsetI)
			}
		}

		if len(errors) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"errors": errors, "data": []M{}})
			return
		}

		var results []map[string]interface{}
		q.Find(&results)
		fmt.Println("RESULTS: ", results)
		c.JSON(http.StatusOK, gin.H{"data": results, "errors": errors})
	})

	r.GET(pathItem, func(c *gin.Context) {
		var item M
		id, err := getIDParam(c)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err})
		}

		if err = DB.First(&item, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"data": item})
	})

	r.POST(path, func(c *gin.Context) {
		var input M
		if err := c.BindJSON(&input); err != nil {
			ve, ok := err.(validator.ValidationErrors)
			if !ok {
				c.JSON(http.StatusInternalServerError, nil)
			}

			errors := []string{}
			for _, e := range ve {
				errors = append(errors, fmt.Sprintf("%v : failed on tag validation: %v", e.Field(), e.ActualTag()))
			}

			c.JSON(http.StatusBadRequest, gin.H{"errors": errors})

			return
		}

		if err := DB.Create(&input).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"errors": []string{err.Error()}})
		} else {
			c.JSON(http.StatusCreated, gin.H{"data": input, "errors": []string{}})
		}
	})

	r.PUT(pathItem, func(c *gin.Context) {
		var input M
		var item M
		id, err := getIDParam(c)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err})
		}

		if err := c.BindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err = DB.First(&item, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
			return
		}

		if err := DB.Model(&item).Updates(input).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusNoContent, nil)
		}
	})

	r.DELETE(pathItem, func(c *gin.Context) {
		var item M
		id, err := getIDParam(c)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err})
		}

		if err := DB.Delete(&item, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
			return
		}

		c.JSON(http.StatusNoContent, nil)
	})
}

func setupRouter() *gin.Engine {
	r := gin.Default()

	// Healthcheck test
	r.GET("/healthcheck", func(c *gin.Context) {
		c.String(http.StatusOK, "Ok")
	})

	return r
}

func registerModels(r *gin.Engine) {
	registerModel(r, User{}, "/users")
}

func main() {
	r := setupRouter()
	registerModels(r)
	// Listen and Server in 0.0.0.0:8080
	ConnectDatabase("root:secret@/testdb?charset=utf8&parseTime=True&loc=Local")
	DB.AutoMigrate(&User{})
	r.Run(":8080")
}
