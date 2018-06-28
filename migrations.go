package migrations

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-pg/pg"
	"github.com/pkg/errors"
)

type migration struct {
	Name string
	Up   func(*pg.DB) error
	Down func(*pg.DB) error
}

var migrationTableName = "public.hb_migrations"
var initialMigration = "000000000000_init"
var allMigrations = make(map[string]migration)
var migrationNames []string

func SetMigrationTableName(tableName string) {
	migrationTableName = tableName
}

func SetInitialMigration(migrationName string) {
	initialMigration = migrationName
}

func Register(name string, up, down func(*pg.DB) error) {
	migrationNames = append(migrationNames, name)

	allMigrations[name] = migration{
		Name: name,
		Up:   up,
		Down: down,
	}
}

func Run(db *pg.DB, a ...string) error {
	var cmd string
	if len(a) > 0 {
		cmd = a[0]
	}

	switch cmd {
	case "init":
		return initialise(db)
	case "migrate":
		return migrate(db)
	case "rollback":
		return rollback(db)
	case "create":
		if len(a) < 2 {
			return errors.New("Please enter migration description")
		}
		return create(strings.Join(a[1:], "_"))
	default:
		return errors.Errorf("unsupported command: %q", cmd)

	}
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
					fmt.Println("FUCK", migration)
					err = errors.New("Initial migration not found")
					return
				}

				err = m.Up(db)

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

func migrate(db *pg.DB) error {
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

		migrationsToRun := difference(migrationNames, migrations)

		if len(migrationsToRun) > 0 {
			var batch int
			batch, err = getBatchNumber(tx)

			if err != nil {
				return
			}

			batch++

			sort.Slice(migrationsToRun, func(i, j int) bool {
				switch strings.Compare(migrationsToRun[i], migrationsToRun[j]) {
				case -1:
					return true
				case 1:
					return false
				}
				return true
			})

			fmt.Printf("Batch %d run: %d migrations\n", batch, len(migrationsToRun))

			for _, migration := range migrationsToRun {
				err = allMigrations[migration].Up(db)

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
				err = allMigrations[migration].Down(db)

				if err != nil {
					return
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

func create(description string) error {

	filename := fmt.Sprintf("%s_%s", time.Now().Format("20060102150405"), description)
	funcName := strings.Replace(strings.Title(strings.Replace(filename, "_", " ", -1)), " ", "", -1)

	err := createMigrationFile(filename, funcName)
	if err != nil {
		return err
	}

	fmt.Println("Created migration", filename)
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

func createMigrationFile(filename, funcName string) error {
	basepath, err := os.Getwd()
	if err != nil {
		return err
	}
	filePath := path.Join(basepath, filename)

	_, err = os.Stat(filePath)
	if !os.IsNotExist(err) {
		return fmt.Errorf("file=%q already exists (%s)", filename, err)
	}

	return ioutil.WriteFile(filePath+".go", []byte(fmt.Sprintf(migrationTemplate, filename, funcName, funcName, funcName, funcName)), 0644)
}

var migrationTemplate = `package main

import (
	"github.com/go-pg/pg"
	migrations "github.com/hbarnardt/hb_migrations"
)

func init() {
	migrations.Register(
		"%s",
		up%s,
		down%s,
	)
}

func up%s(db *pg.DB) error {
	return nil
}

func down%s(db *pg.DB) error {
	return nil
}
`