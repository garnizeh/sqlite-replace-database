package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	_ "github.com/mattn/go-sqlite3"
)

const (
	updateChannel = "update-data"
	dbName        = "data"
	newName       = "new"
)

var (
	id = uuid.NewString()
)

func main() {
	println("starting id:", id)

	svc := newService(dbName)
	defer func() {
		if err := svc.close(); err != nil {
			println("main close db err:", err.Error())
		}
	}()

	redisC := redis.NewClient(&redis.Options{
		Addr: "redis:6379",
	})

	ctx := context.Background()
	sub := redisC.Subscribe(ctx, updateChannel)
	go checkUpdate(ctx, sub, svc)

	app := fiber.New()
	app.Get("/read/:id", read(svc))
	app.Get("/update", update(redisC))

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	var serverShutdown sync.WaitGroup

	go func() {
		<-c
		println("Gracefully shutting down...")
		serverShutdown.Add(1)
		defer serverShutdown.Done()

		_ = app.ShutdownWithTimeout(60 * time.Second)
	}()

	if err := app.Listen(":8080"); err != nil {
		panic(err)
	}

	serverShutdown.Wait()
	println("Running cleanup tasks...")

	if err := app.Listen(":8080"); err != nil {
		panic(err)
	}
}

type service struct {
	filename    string
	db          *sql.DB
}

func newService(name string) *service {
	filename := getFilename(name)
	db := getDB(filename)

	return &service{
		filename: filename,
		db:       db,
	}
}

func (s *service) update(filename string) bool {
	if err := s.db.Close(); err != nil {
		println("service update close db err:", err.Error())
		return false
	}

	s.db = getDB(filename)
	return true
}

func (s *service) close() error {
	return s.db.Close()
}

func (s *service) read(id string) (string, error) {
	var (
		stmt *sql.Stmt
		err  error
	)

	for range 100 {
		stmt, err = s.db.Prepare("select c from a where b = ?")
		if err == nil {
			break
		}

		time.Sleep(time.Millisecond * 5)
	}
	if err != nil {
		return "", fmt.Errorf("prepare err: %w", err)
	}

	defer stmt.Close()

	var value string
	if err := stmt.QueryRow(id).Scan(&value); err != nil {
		return "", fmt.Errorf("scan err: %w", err)
	}

	return value, nil
}

type msgUpdate struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

func checkUpdate(ctx context.Context, sub *redis.PubSub, svc *service) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := sub.ReceiveMessage(ctx)
		if err != nil {
			println("sub err:", err.Error())
			continue
		}

		var payload msgUpdate
		if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
			panic(fmt.Sprintf("message [%q] unmarshal err: %v", msg.Payload, err))
		}

		fmt.Printf("sub rcvd msg: %+v", payload)
		filename := getFilename(payload.Name)
		if !svc.update(filename) {
			panic("failed to update db connection")
		}

		println("db connection updated")
		if payload.Id != id {
			continue
		}

		go func() {
			time.Sleep(5 * time.Second)

			if err := os.Remove(svc.filename); err != nil {
				println("remove old db err:", err.Error())
				return
			}

			if err := os.Rename(filename, svc.filename); err != nil {
				println("rename new db err:", err.Error())
				return
			}
			if err := os.Rename(filename+"-shm", svc.filename+"-shm"); err != nil {
				println("rename new db err:", err.Error())
				return
			}
			if err := os.Rename(filename+"-wal", svc.filename+"-wal"); err != nil {
				println("rename new db err:", err.Error())
				return
			}

			println("db updated")
		}()
	}
}

func getFilename(name string) string {
	return fmt.Sprintf("/data/%s.db", name)
}

func getDB(filename string) *sql.DB {
	println("getDB", filename)
	db, err := sql.Open("sqlite3", fmt.Sprintf("%s?_journal=wal&_txlock=immediate&_busy_timeout=10000", filename))
	if err != nil {
		panic(err)
	}

	db.SetMaxOpenConns(1)

	var tx *sql.Tx
	for range 400 {
		time.Sleep(time.Millisecond * 25)
		tx, err = db.Begin()
		if err == nil {
			break
		}
	}
	if err != nil {
		panic(err)
	}

	if _, err := tx.Exec("CREATE TABLE IF NOT EXISTS a (b INT, c TEXT)"); err != nil {
		panic(err)
	}

	if _, err := tx.Exec(fmt.Sprintf("INSERT OR IGNORE INTO a (b, c) VALUES (1, %q)", filename)); err != nil {
		panic(err)
	}

	err = tx.Commit()
	if err != nil {
		panic(err)
	}

	return db
}

func read(svc *service) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		key := c.Params("id")

		value, err := svc.read(key)
		if err != nil {
			println(err.Error())
			if errors.Is(err, sql.ErrNoRows) {
				return c.SendStatus(fiber.StatusNotFound)
			}

			return c.SendStatus(fiber.StatusInternalServerError)
		}

		return c.SendString(fmt.Sprintf("[%s] %q", id, value))
	}
}

func update(redisC *redis.Client) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		payload := msgUpdate{
			Id:   id,
			Name: fmt.Sprintf("%s-%d", newName, time.Now().UTC().UnixMicro()),
		}

		msg, err := json.Marshal(payload)
		if err != nil {
			println("json marshal payload err:", err.Error())
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		if err := redisC.Publish(c.Context(), updateChannel, msg).Err(); err != nil {
			println("redis publish err:", err.Error())
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		println(string(msg))
		return c.SendStatus(fiber.StatusOK)
	}
}
