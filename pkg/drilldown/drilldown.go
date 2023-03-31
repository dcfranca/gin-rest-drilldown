package drilldown

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-sql-driver/mysql"
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

type ApiConfig struct {
	LookupField string
}

var DB *gorm.DB

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

func getIDParamInt(c *gin.Context, lookupField string) (uint64, error) {
	idS, ok := c.Params.Get(lookupField)
	if !ok {
		return 0, fmt.Errorf("invalid ID (%v)", lookupField)
	}

	id, err := strconv.Atoi(idS)
	if err != nil {
		return 0, fmt.Errorf("invalid ID (%v) %v", lookupField, err)
	}

	return uint64(id), nil
}

func getIDParamString(c *gin.Context, lookupField string) (*string, error) {
	id, ok := c.Params.Get(lookupField)
	if !ok {
		return nil, fmt.Errorf("invalid ID (%v)", lookupField)
	}

	return &id, nil
}

func GetItem[M any](c *gin.Context, config *ApiConfig) (error, *M, uint64, *string) {
	var item M
	var idInt uint64
	var idString *string
	var err error

	v := reflect.ValueOf(item)

	lookupField := "ID"
	if config != nil && config.LookupField != "" {
		lookupField = config.LookupField
	}
	lowerLookupField := strings.ToLower(lookupField)

	if v.FieldByName(lookupField).Type().Name() == "uint64" {
		idInt, err = getIDParamInt(c, lowerLookupField)
	} else {
		idString, err = getIDParamString(c, lowerLookupField)
	}

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err})
		return err, nil, idInt, idString
	}

	whereClause := fmt.Sprintf("%s = ?", lowerLookupField)

	if idString != nil {
		if err = DB.WithContext(c).Where(whereClause, idString).First(&item).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
			return err, nil, idInt, idString
		}
	} else {
		if err = DB.WithContext(c).Where(whereClause, idInt).First(&item, idInt).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
			return err, nil, idInt, idString
		}
	}

	return nil, &item, idInt, idString
}

func removePlural(s string) string {
	if strings.HasSuffix(s, "s") {
		return s[:len(s)-1]
	}

	return s
}

func IsTestRun() bool {
	f := flag.Lookup("test.v")
	if f == nil {
		return false
	}

	return f.Value.(flag.Getter).Get().(bool)
}

func RegisterModel[M any](r *gin.Engine, m M, resource string, config *ApiConfig) {

	path := "/" + resource
	lookupField := "id"
	if config != nil && config.LookupField != "" {
		lookupField = strings.ToLower(config.LookupField)
	}
	pathItem := fmt.Sprintf("%v/:%v", path, lookupField)

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
		err, item, _, _ := GetItem[M](c, config)
		if err != nil {
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
				return
			}

			errors := []string{}
			for _, e := range ve {
				errors = append(errors, fmt.Sprintf("%v : failed on tag validation: %v", e.Field(), e.ActualTag()))
			}

			c.JSON(http.StatusBadRequest, gin.H{"errors": errors})
			return
		}

		if err := DB.WithContext(c).Create(&input).Error; err != nil {
			me, ok := err.(*mysql.MySQLError)
			if !ok {
				c.JSON(http.StatusInternalServerError, nil)
				return
			}

			errors := []string{fmt.Sprintf("Error %v: %v", me.Number, me.Message)}
			c.JSON(http.StatusBadRequest, gin.H{"errors": errors})
			return
		} else {
			c.JSON(http.StatusCreated, gin.H{"data": input, "errors": []string{}})
			return
		}
	})

	r.PUT(pathItem, func(c *gin.Context) {
		var input M
		err, item, _, _ := GetItem[M](c, config)
		if err != nil {
			return
		}

		c.ShouldBindJSON(&input)

		if err := DB.WithContext(c).Model(&item).Updates(input).Error; err != nil {
			me, ok := err.(*mysql.MySQLError)
			if !ok {
				c.JSON(http.StatusInternalServerError, nil)
				return
			}

			errors := []string{fmt.Sprintf("Error %v: %v", me.Number, me.Message)}
			c.JSON(http.StatusBadRequest, gin.H{"errors": errors})
			return
		} else {
			c.JSON(http.StatusNoContent, nil)
		}
	})

	r.DELETE(pathItem, func(c *gin.Context) {
		err, item, idInt, idStr := GetItem[M](c, config)
		if err != nil {
			return
		}

		lookupField := "ID"
		if config != nil && config.LookupField != "" {
			lookupField = config.LookupField
		}
		lowerLookupField := strings.ToLower(lookupField)
		whereClause := fmt.Sprintf("%s = ?", lowerLookupField)

		if idStr != nil {
			if err := DB.WithContext(c).Where(whereClause, idStr).Delete(&item).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
				return
			}
		} else {
			if err := DB.WithContext(c).Where(whereClause, idInt).Delete(&item).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "Record not found!"})
				return
			}
		}

		c.JSON(http.StatusNoContent, nil)
	})
}

func SetupRouter() *gin.Engine {
	r := gin.Default()

	// Healthcheck test
	r.GET("/healthcheck", func(c *gin.Context) {
		c.String(http.StatusOK, "Ok")
	})

	return r
}
