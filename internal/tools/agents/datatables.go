package agents

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/datatable"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// globalDataTables is the singleton in-memory service shared between
// the workflow engine (datatable_* nodes), the Data Tables UI, and
// MCP ops. Wired by setup.Manager.DataTables at boot via
// SetDataTables.
var globalDataTables datatable.Service

// SetDataTables registers the shared data-table service. nil is
// allowed during early boot; handlers fall back to 503.
func SetDataTables(s datatable.Service) { globalDataTables = s }

func dataTablesNotReady(c *tool.Ctx) bool {
	if globalDataTables == nil {
		c.Error(http.StatusServiceUnavailable, "data tables service not initialised")
		return true
	}
	return false
}

// dataTablesPage lists every registered table.
func dataTablesPage(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	vm := view.DataTablesVM{
		Layout: sidebarVM(c, "data-tables", ""),
		Base:   c.Base(),
		Flash:  c.Query("flash"),
	}
	for _, slug := range globalDataTables.ListTables() {
		sc, err := globalDataTables.LoadSchema(slug)
		if err != nil {
			continue
		}
		count, _ := globalDataTables.Count(slug, nil)
		vm.Tables = append(vm.Tables, view.DataTableRow{
			Slug:      slug,
			Name:      sc.Name,
			RowCount:  count,
			Columns:   len(sc.Columns),
			SizeBytes: int64(count) * 256, // rough estimate; replaced by real bytes when Postgres backend lands
			UpdatedAt: sc.UpdatedAt,
			CreatedAt: sc.CreatedAt,
		})
	}
	c.HTML(view.DataTablesPage(vm))
}

// listDataTablesJSON returns [{slug, name}] for the workflow inspector dropdown.
func listDataTablesJSON(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	type item struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	var out []item
	for _, slug := range globalDataTables.ListTables() {
		sc, err := globalDataTables.LoadSchema(slug)
		if err != nil {
			continue
		}
		out = append(out, item{Slug: slug, Name: sc.Name})
	}
	if out == nil {
		out = []item{}
	}
	c.JSON(200, out)
}

// listDataTableColumnsJSON returns [{name, type}] for user-defined columns
// of a table (system columns id/created_at/updated_at excluded).
func listDataTableColumnsJSON(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	sc, err := globalDataTables.LoadSchema(slug)
	if err != nil {
		c.JSON(200, []any{})
		return
	}
	type col struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var out []col
	for _, column := range sc.Columns {
		if column.System {
			continue
		}
		out = append(out, col{Name: column.Name, Type: column.Type})
	}
	if out == nil {
		out = []col{}
	}
	c.JSON(200, out)
}

// createDataTable handles POST /data-tables (new-table modal).
// Supports two flows (n8n parity): scratch (name + optional advanced
// columns) and csv (file upload, headers become columns).
func createDataTable(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	name := strings.TrimSpace(c.Form("name"))
	if name == "" {
		c.Error(http.StatusBadRequest, "name is required")
		return
	}
	slug := slugify(name)
	if slug == "" {
		c.Error(http.StatusBadRequest, "name must contain at least one letter or digit")
		return
	}

	mode := c.Form("create_mode")
	if mode == "" {
		mode = "scratch"
	}

	if mode == "csv" {
		file, _, err := c.R.FormFile("csv")
		if err != nil {
			c.Error(http.StatusBadRequest, "CSV file is required for Import CSV mode")
			return
		}
		defer file.Close()
		if err := importCSVInto(slug, name, file); err != nil {
			c.Error(http.StatusBadRequest, err.Error())
			return
		}
		c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
		return
	}

	// scratch flow.
	dtMode := c.Form("mode")
	if dtMode == "" {
		dtMode = datatable.ModeStrict
	}
	pk := strings.TrimSpace(c.Form("primary_key"))
	if pk == "" {
		pk = "id"
	}
	cols, err := parseColumnsText(c.Form("columns"), pk)
	if err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	sc := datatable.Schema{
		Slug:       slug,
		Name:       name,
		Mode:       dtMode,
		PrimaryKey: []string{pk},
		Columns:    cols,
	}
	if err := globalDataTables.CreateTable(sc); err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
}

// renameDataTableColumn handles POST /data-tables/{slug}/columns/{col}/rename.
func renameDataTableColumn(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	from := c.PathValue("col")
	to := strings.TrimSpace(c.Form("name"))
	if err := globalDataTables.RenameColumn(slug, from, to); err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
}

// dropDataTableColumn handles POST /data-tables/{slug}/columns/{col}/delete.
func dropDataTableColumn(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	col := c.PathValue("col")
	if err := globalDataTables.DropColumn(slug, col); err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
}

// addDataTableColumn appends one column to an existing table's schema.
func addDataTableColumn(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	sc, err := globalDataTables.LoadSchema(slug)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	name := strings.TrimSpace(c.Form("name"))
	typ := strings.TrimSpace(c.Form("type"))
	if name == "" {
		c.Error(http.StatusBadRequest, "column name required")
		return
	}
	if typ == "" {
		typ = "string"
	}
	for _, col := range sc.Columns {
		if col.Name == name {
			c.Error(http.StatusBadRequest, "column already exists: "+name)
			return
		}
	}
	sc.Columns = append(sc.Columns, datatable.Column{Name: name, Type: typ})
	if err := globalDataTables.SaveSchema(sc); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
}

// dropDataTable removes a table + all its rows.
func dropDataTable(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	if err := globalDataTables.DropTable(slug); err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	c.Redirect(c.Base()+"/data-tables?flash=Dropped+"+slug, http.StatusSeeOther)
}

// dataTableDetail shows schema + rows. Sort + per-column filters are
// driven from query params so reload preserves state and bookmarks
// work:
//
//	?sort=<col>:asc|desc                  — single column sort
//	?f.<col>.op=<op>&f.<col>.v=<value>    — per-column filter chip
//
// The sort/filter state is round-tripped to the VM so headers render
// the active indicator and the popover pre-fills last-applied value.
func dataTableDetail(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	sc, err := globalDataTables.LoadSchema(slug)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	sortCol, sortDir := parseSortQuery(c.Query("sort"))
	filters := parseFilterQuery(c.R.URL.Query())
	order := orderForSort(sc, sortCol, sortDir)
	conds := conditionsForFilters(sc, filters)
	var rows []map[string]any
	if len(conds) > 0 {
		rows, err = globalDataTables.QueryConditions(slug, conds, order, 0, 0)
	} else {
		rows, err = globalDataTables.Query(slug, nil, order, 0, 0)
	}
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.HTML(view.DataTableDetailPage(view.DataTableDetailVM{
		Layout:  sidebarVM(c, "data-tables", ""),
		Base:    c.Base(),
		Slug:    slug,
		Schema:  sc,
		Rows:    rows,
		Flash:   c.Query("flash"),
		SortCol: sortCol,
		SortDir: sortDir,
		Filters: filters,
	}))
}

// parseSortQuery splits "col:dir" into (col, dir). Empty input → empty
// result so the caller can skip ORDER BY override.
func parseSortQuery(raw string) (col, dir string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if idx := strings.IndexByte(raw, ':'); idx > 0 {
		col = raw[:idx]
		dir = strings.ToLower(strings.TrimSpace(raw[idx+1:]))
	} else {
		col = raw
		dir = "asc"
	}
	if dir != "asc" && dir != "desc" {
		dir = "asc"
	}
	return col, dir
}

// parseFilterQuery extracts per-column filter chips from query params
// shaped as `f.<col>.op=<op>` and `f.<col>.v=<value>`. Op alone with
// no value is allowed for is_empty / is_not_empty.
func parseFilterQuery(q map[string][]string) map[string]view.FilterChip {
	out := map[string]view.FilterChip{}
	for key, vals := range q {
		if !strings.HasPrefix(key, "f.") {
			continue
		}
		rest := key[2:]
		dot := strings.LastIndexByte(rest, '.')
		if dot < 0 {
			continue
		}
		col := rest[:dot]
		field := rest[dot+1:]
		if len(vals) == 0 {
			continue
		}
		chip := out[col]
		switch field {
		case "op":
			chip.Op = vals[0]
		case "v":
			chip.Value = vals[0]
		}
		out[col] = chip
	}
	// Drop entries that have neither an op nor a value (browser quirk).
	for k, v := range out {
		if v.Op == "" && v.Value == "" {
			delete(out, k)
		}
	}
	return out
}

// orderForSort translates a sort query into a single DataTableOrder.
// Unknown columns return nil so callers fall back to id ASC.
func orderForSort(sc datatable.Schema, col, dir string) []workflow.DataTableOrder {
	if col == "" {
		return nil
	}
	if !columnExists(sc, col) {
		return nil
	}
	return []workflow.DataTableOrder{{Column: col, Direction: dir}}
}

// conditionsForFilters builds a Condition list from the chip map.
// Chips with empty op are skipped; is_empty / is_not_empty ignore the
// value field.
func conditionsForFilters(sc datatable.Schema, filters map[string]view.FilterChip) []datatable.Condition {
	if len(filters) == 0 {
		return nil
	}
	out := make([]datatable.Condition, 0, len(filters))
	for col, f := range filters {
		if !columnExists(sc, col) {
			continue
		}
		if f.Op == "" {
			continue
		}
		c := datatable.Condition{Column: col, Op: f.Op}
		if f.Op != datatable.OpIsEmpty && f.Op != datatable.OpIsNotEmpty {
			if f.Value == "" {
				continue // skip incomplete chip
			}
			c.Value = f.Value
		}
		out = append(out, c)
	}
	return out
}

// columnExists reports whether the schema has a user column or system
// column with the given name. Used to drop unknown query params from
// untrusted clients.
func columnExists(sc datatable.Schema, name string) bool {
	if datatable.IsSystemColumn(name) {
		return true
	}
	for _, c := range sc.Columns {
		if c.Name == name && !c.System {
			return true
		}
	}
	return false
}

// insertDataTableRow handles POST /data-tables/{slug}/rows (add-row modal).
func insertDataTableRow(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	sc, err := globalDataTables.LoadSchema(slug)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	row := map[string]any{}
	for _, col := range sc.Columns {
		raw := strings.TrimSpace(c.Form("col_" + col.Name))
		if raw == "" {
			continue
		}
		row[col.Name] = coerceValue(col.Type, raw)
	}
	if err := globalDataTables.Insert(slug, row); err != nil {
		c.Redirect(c.Base()+"/data-tables/"+slug+"?flash="+urlQueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
}

// bulkDeleteDataTableRows removes every row whose id appears in the
// repeated `ids` form field. Used by the spreadsheet's selection
// toolbar — Delete button posts the selected checkboxes here in one
// request so the user gets a single round-trip instead of N.
func bulkDeleteDataTableRows(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	if _, err := globalDataTables.LoadSchema(slug); err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	ids := c.R.Form["ids"]
	if len(ids) == 0 {
		_ = c.R.ParseForm()
		ids = c.R.Form["ids"]
	}
	deleted := 0
	for _, raw := range ids {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		n, err := globalDataTables.Delete(slug, map[string]any{datatable.ColID: id})
		if err != nil {
			c.Error(http.StatusInternalServerError, err.Error())
			return
		}
		deleted += n
	}
	c.Redirect(c.Base()+"/data-tables/"+slug+"?flash=Deleted+"+strconv.Itoa(deleted)+"+row(s)", http.StatusSeeOther)
}

// deleteDataTableRow removes one row by joined PK.
func deleteDataTableRow(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	pkParam := c.PathValue("pk")
	sc, err := globalDataTables.LoadSchema(slug)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	parts := strings.Split(pkParam, "|")
	if len(parts) != len(sc.PrimaryKey) {
		c.Error(http.StatusBadRequest, "primary key shape mismatch")
		return
	}
	where := map[string]any{}
	for i, k := range sc.PrimaryKey {
		where[k] = parts[i]
	}
	if _, err := globalDataTables.Delete(slug, where); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
}

// importDataTableCSV creates a table from an uploaded CSV file (legacy
// "Upload CSV" button on the list page). Slug derived from filename.
func importDataTableCSV(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	file, header, err := c.R.FormFile("file")
	if err != nil {
		c.Error(http.StatusBadRequest, "no file uploaded")
		return
	}
	defer file.Close()
	slug := slugify(strings.TrimSuffix(header.Filename, ".csv"))
	if slug == "" {
		slug = "imported"
	}
	if err := importCSVInto(slug, slug, file); err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}
	c.Redirect(c.Base()+"/data-tables/"+slug, http.StatusSeeOther)
}

// importCSVInto materialises a data table from a CSV stream. First row =
// headers, primary key inferred as the first column, all columns typed
// `string`. Used by both the list-page Upload CSV button and the
// "Import CSV" branch of the create modal.
func importCSVInto(slug, name string, file io.Reader) error {
	reader := csv.NewReader(file)
	headerRow, err := reader.Read()
	if err != nil {
		return fmt.Errorf("empty CSV: %w", err)
	}
	cols := make([]datatable.Column, 0, len(headerRow))
	for _, h := range headerRow {
		cols = append(cols, datatable.Column{Name: strings.TrimSpace(h), Type: "string"})
	}
	if len(cols) == 0 {
		return fmt.Errorf("CSV has no columns")
	}
	pk := cols[0].Name
	sc := datatable.Schema{
		Slug:       slug,
		Name:       name,
		Mode:       datatable.ModeLax,
		PrimaryKey: []string{pk},
		Columns:    cols,
	}
	if err := globalDataTables.CreateTable(sc); err != nil {
		_ = globalDataTables.SaveSchema(sc)
	}
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
		m := map[string]any{}
		for i, v := range row {
			if i >= len(cols) {
				break
			}
			m[cols[i].Name] = v
		}
		_ = globalDataTables.Insert(slug, m)
	}
	return nil
}

// exportDataTableCSV streams the whole table as CSV.
func exportDataTableCSV(c *tool.Ctx) {
	if dataTablesNotReady(c) {
		return
	}
	slug := c.PathValue("slug")
	sc, err := globalDataTables.LoadSchema(slug)
	if err != nil {
		c.Error(http.StatusNotFound, err.Error())
		return
	}
	rows, err := globalDataTables.Query(slug, nil, nil, 0, 0)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	w := c.W
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, slug))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	cols := make([]string, len(sc.Columns))
	for i, col := range sc.Columns {
		cols[i] = col.Name
	}
	_ = cw.Write(cols)
	for _, r := range rows {
		out := make([]string, len(cols))
		for i, name := range cols {
			if v, ok := r[name]; ok && v != nil {
				out[i] = fmt.Sprintf("%v", v)
			}
		}
		_ = cw.Write(out)
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func parseColumnsText(raw, pk string) ([]datatable.Column, error) {
	lines := strings.Split(raw, "\n")
	out := []datatable.Column{}
	pkPresent := false
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		parts := strings.SplitN(l, ":", 2)
		name := strings.TrimSpace(parts[0])
		typ := "string"
		if len(parts) == 2 {
			typ = strings.TrimSpace(parts[1])
		}
		if name == "" {
			continue
		}
		col := datatable.Column{Name: name, Type: typ}
		if name == pk {
			col.Required = true
			pkPresent = true
		}
		out = append(out, col)
	}
	if !pkPresent {
		// auto-insert pk column at the front
		out = append([]datatable.Column{{Name: pk, Type: "string", Required: true}}, out...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one column required")
	}
	return out, nil
}

func coerceValue(typ, raw string) any {
	switch typ {
	case "int":
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n
		}
	case "float":
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return f
		}
	case "bool":
		switch strings.ToLower(raw) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return raw
}

// slugify turns an arbitrary filename into a [a-z0-9-] slug.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func urlQueryEscape(s string) string {
	return strings.NewReplacer(" ", "+", "&", "%26", "?", "%3F", "#", "%23").Replace(s)
}
