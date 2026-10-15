package httpapi

import (
	"entertainmentdistrict/internal/cinemeta"
	"net/http"
	"strconv"
)

func catalogFilters(r *http.Request) (cinemeta.Filters, error) {
	q := r.URL.Query()
	f := cinemeta.Filters{Catalog: q.Get("catalog"), Genre: q.Get("genre")}
	if q.Get("year") != "" {
		n, err := strconv.Atoi(q.Get("year"))
		if err != nil {
			return f, cinemeta.ErrInvalid
		}
		f.Year = n
	}
	return f, nil
}
