package migrations

import (
	"bytes"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/go-pg/pg/v9"
	"github.com/pkg/errors"
)

type migration struct {
	Name string
	Up   func(*pg.Tx) error
	Down func(*pg.Tx) error
}

type MigrationNameConvention string

const (
	CamelCase MigrationNameConvention = "camelCase"
	SnakeCase MigrationNameConvention = "snakeCase"
)

var migrationTableName = "public.hb_migrations"
var initialMigration = "000000000000_init"
var migrationNameConvention = SnakeCase
var allMigrations = make(map[string]migration)
var migrationNames []string

func SetMigrationTableName(tableName string) {
	migrationTableName = tableName
}

func SetInitialMigration(migrationName string) {
	initialMigration = migrationName
}

func SetMigrationNameConvention(convention MigrationNameConvention) {
	migrationNameConvention = convention
}

func Register(name string, up, down func(*pg.Tx) error) {
	migrationNames = append(migrationNames, name)

	allMigrations[name] = migration{
		Name: name,
		Up:   up,
		Down: down,
	}
}

/*
Run Runs the specified command with the options they require
Note:
	init - no options
	migrate - one option
		- "" for all migrations in a single batch
		- "one-by-one" for one migration in a batch mode
	rollback - no options
	create - two options
		- name - name of the migration (must be first)
		- template - string that contains the go code to use as a template. see migrationTemplate
*/
func Run(db *pg.DB, cmd string, options ...string) error {
	switch cmd {
	case "init":
		return initialise(db)

	case "migrate":
		extra := ""
		if len(options) > 0 {
			extra = options[0]
		}
		return migrate(db, extra == "one-by-one")

	case "rollback":
		return rollback(db)

	case "create":
		name := ""
		template := ""
		if len(options) > 0 {
			name = options[0]
		}
		if len(options) > 1 {
			template = options[1]
		}
		if len(name) == 0 {
			return errors.New("Please enter migration name")
		}

		name = strings.Replace(name, " ", "_", -1)

		return create(name, template)
	}

	return errors.Errorf("unsupported command: %q", cmd)
}

func initialise(db *pg.DB) error {
	return db.RunInTransaction(func(tx *pg.Tx) (err error) {

		err = lockTable(tx)

		if err != nil {
			return
		}

		migrationsToRun := []string{initialMigration}

		if len(migrationsToRun) > 0 {
			var batch int
			batch, err = getBatchNumber(tx)

			if err != nil {
				return
			}

			batch++

			fmt.Printf("Batch %d run: %d migrations\n", batch, len(migrationsToRun))

			for _, migration := range migrationsToRun {
				m, ok := allMigrations[migration]

				if !ok {
					err = errors.New("Initial migration not found")
					return
				}

				err = m.Up(tx)

				if err != nil {
					return
				}

				err = insertCompletedMigration(tx, migration, batch)

				if err != nil {
					return
				}
			}
		}
		return
	})
}

func getMigrationsToRun(tx *pg.Tx) ([]string, error) {
	var migrations []string

	migrations, err := getCompletedMigrations(tx)
	if err != nil {
		return nil, err
	}

	missingMigrations := difference(migrations, migrationNames)
	if len(missingMigrations) > 0 {
		return nil, errors.Errorf("Migrations table corrupt: %+v", missingMigrations)
	}

	migrationsToRun := difference(migrationNames, migrations)

	if len(migrationsToRun) > 0 {
		sort.Strings(migrationsToRun)
	}

	return migrationsToRun, nil
}
func migrate(db *pg.DB, oneByOne bool) error {
	if oneByOne {
		return migrateOneByOne(db)
	}
	return migrateOneBatch(db)
}

func migrateOneByOne(db *pg.DB) error {

	var migrationsToRun []string

	err := db.RunInTransaction(
		func(tx *pg.Tx) (err error) {
			err = lockTable(tx)
			if err != nil {
				return
			}

			migrationsToRun, err = getMigrationsToRun(tx)
			return
		})

	if err != nil {
		return err
	}

	if len(migrationsToRun) == 0 {
		return nil
	}

	for _, migration := range migrationsToRun {
		err := db.RunInTransaction(
			func(tx *pg.Tx) (err error) {
				err = lockTable(tx)
				if err != nil {
					return
				}

				var batch int
				batch, err = getBatchNumber(tx)
				if err != nil {
					return
				}

				batch++

				fmt.Printf("Batch %d run: 1 migration - %s\n", batch, migration)

				err = allMigrations[migration].Up(tx)
				if err != nil {
					err = errors.Wrapf(err, "%s failed to migrate", migration)
					return
				}

				err = insertCompletedMigration(tx, migration, batch)
				return
			})
		if err != nil {
			return err
		}
	}

	return nil
}

func migrateOneBatch(db *pg.DB) error {
	return db.RunInTransaction(func(tx *pg.Tx) (err error) {

		err = lockTable(tx)
		if err != nil {
			return
		}

		var migrationsToRun []string
		migrationsToRun, err = getMigrationsToRun(tx)
		if err != nil {
			return
		}

		if len(migrationsToRun) == 0 {
			return
		}

		var batch int
		batch, err = getBatchNumber(tx)
		if err != nil {
			return
		}

		batch++

		fmt.Printf("Batch %d run: %d migrations\n", batch, len(migrationsToRun))

		for _, migration := range migrationsToRun {
			err = allMigrations[migration].Up(tx)

			if err != nil {
				err = errors.Wrapf(err, "%s failed to migrate", migration)
				return
			}

			err = insertCompletedMigration(tx, migration, batch)

			if err != nil {
				return
			}
		}

		return
	})
}

func rollback(db *pg.DB) error {
	return db.RunInTransaction(func(tx *pg.Tx) (err error) {

		err = lockTable(tx)

		if err != nil {
			return
		}

		var migrations []string

		migrations, err = getCompletedMigrations(tx)

		if err != nil {
			return
		}

		missingMigrations := difference(migrations, migrationNames)

		if len(missingMigrations) > 0 {
			return errors.New("Migrations table corrupt")
		}

		var batch int
		batch, err = getBatchNumber(tx)

		if err != nil {
			return
		}

		migrationsToRun, err := getMigrationsInBatch(tx, batch)

		if err != nil {
			return
		}

		if len(migrationsToRun) > 0 {
			sort.Slice(migrationsToRun, func(i, j int) bool {
				switch strings.Compare(migrationsToRun[i], migrationsToRun[j]) {
				case -1:
					return false
				case 1:
					return true
				}
				return false
			})

			fmt.Printf("Batch %d rollback: %d migrations\n", batch, len(migrationsToRun))

			for _, migration := range migrationsToRun {
				err = allMigrations[migration].Down(tx)

				if err != nil {
					err = errors.Wrapf(err, "%s failed to rollback", migration)
					break
				}

				err = removeRolledbackMigration(tx, migration)

				if err != nil {
					return
				}
			}
		}
		return
	})
}

func create(description, template string) error {
	var filename, funcName string

	if migrationNameConvention == SnakeCase {
		description = convertCamelCaseToSnakeCase(description)
		filename = fmt.Sprintf("%s_%s", time.Now().Format("20060102150405"), description)
		funcName = convertSnakeCaseToCamelCase(filename)
	} else {
		description = convertSnakeCaseToCamelCase(description)
		filename = fmt.Sprintf("%s%s", time.Now().Format("20060102150405"), description)
		funcName = filename
	}

	filePath, err := createMigrationFile(filename, funcName, template)
	if err != nil {
		return err
	}

	fmt.Println("Created migration", filePath)
	return nil
}

func lockTable(tx *pg.Tx) error {

	_, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS ? (
				id serial,
				name varchar,
				batch integer,
				migration_time timestamptz
			)
		`, pg.Q(migrationTableName))
	if err != nil {
		return err
	}
	_, err = tx.Exec("LOCK ? ", pg.Q(migrationTableName))

	return err
}

func insertCompletedMigration(tx *pg.Tx, name string, batch int) error {
	fmt.Printf("Completed %s\n", name)
	_, err := tx.Exec("insert into ? (name, batch, migration_time) values (?, ?, now())", pg.Q(migrationTableName), name, batch)

	if err != nil {
		return err
	}

	return nil
}

func removeRolledbackMigration(tx *pg.Tx, name string) error {
	fmt.Printf("Rolledback %s\n", name)
	_, err := tx.Exec("delete from ? where name = ?", pg.Q(migrationTableName), name)

	if err != nil {
		return err
	}

	return nil
}

func getCompletedMigrations(tx *pg.Tx) ([]string, error) {
	var results []string

	_, err := tx.Query(&results, "select name from ?", pg.Q(migrationTableName))

	if err != nil {
		return nil, err
	}

	return results, nil
}

func getMigrationsInBatch(tx *pg.Tx, batch int) ([]string, error) {
	var results []string

	_, err := tx.Query(&results, "select name from ? where batch = ? order by id desc", pg.Q(migrationTableName), batch)

	if err != nil {
		return nil, err
	}

	return results, nil
}

func getBatchNumber(tx *pg.Tx) (int, error) {
	var result int

	_, err := tx.Query(&result, "select max(batch) from ?", pg.Q(migrationTableName))

	if err != nil {
		return 0, err
	}

	return result, nil
}

func difference(a, b []string) []string {
	mb := map[string]bool{}
	for _, x := range b {
		mb[x] = true
	}
	ab := []string{}
	for _, x := range a {
		if _, ok := mb[x]; !ok {
			ab = append(ab, x)
		}
	}
	return ab
}

func convertCamelCaseToSnakeCase(word string) (result string) {
	l := 0
	var fields []string
	for s := word; s != ""; s = s[l:] {
		l = strings.IndexFunc(s[1:], unicode.IsUpper) + 1
		if l <= 0 {
			l = len(s)
		}
		fields = append(fields, strings.ToLower(s[:l]))
	}

	result = strings.Join(fields, "_")

	return
}

func convertSnakeCaseToCamelCase(word string) (result string) {
	fields := strings.Split(word, "_")
	for i := 0; i < len(fields); i++ {
		fields[i] = strings.Title(fields[i])
	}

	result = strings.Join(fields, "")

	return
}

func createMigrationFile(filename, funcName, templateString string) (string, error) {
	basepath, err := os.Getwd()
	if err != nil {
		return "", err
	}
	filePath := path.Join(basepath, filename)

	_, err = os.Stat(filePath)
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("file=%s already exists (%v)", filename, err)
	}

	filePath += ".go"

	if len(templateString) == 0 {
		templateString = migrationTemplate
	}

	data := map[string]interface{}{
		"Filename": filename,
		"FuncName": funcName,
	}

	t := template.Must(template.New("template").Parse(templateString))

	buf := &bytes.Buffer{}
	if err := t.Execute(buf, data); err != nil {
		return "", fmt.Errorf("Failed to populate migration template %v", err)
	}

	templateString = buf.String()

	return filePath, ioutil.WriteFile(filePath, []byte(templateString), 0644)
}

var migrationTemplate = `package main

import (
	"github.com/go-pg/pg/v9"
	migrations "github.com/getkalido/hb_migrations"
)

func init() {
	migrations.Register(
		"{{.Filename}}",
		up{{.FuncName}},
		down{{.FuncName}},
	)
}

func up{{.FuncName}}(tx *pg.Tx) error {
	_, err := tx.Exec(` + "`" + "`" + `)
	return err
}

func down{{.FuncName}}(tx *pg.Tx) error {
	_, err := tx.Exec(` + "`" + "`" + `)
	return err
}
`
