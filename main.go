package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/crypto/bcrypt"
)

const (
	ColorReset   = "\x1b[0m"
	ColorInfo    = "\x1b[36m"
	ColorWarn    = "\x1b[33m"
	ColorError   = "\x1b[31m"
	ColorSuccess = "\x1b[32m"
)

func logMessage(level, color, message string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("%s[%s] [%s] %s%s\n", color, timestamp, level, message, ColorReset)
}

func LogInfo(format string, a ...any)  { logMessage("INFO", ColorInfo, fmt.Sprintf(format, a...)) }
func LogWarn(format string, a ...any)  { logMessage("WARN", ColorWarn, fmt.Sprintf(format, a...)) }
func LogError(format string, a ...any) { logMessage("ERROR", ColorError, fmt.Sprintf(format, a...)) }
func LogSuccess(format string, a ...any) {
	logMessage("SUCCESS", ColorSuccess, fmt.Sprintf(format, a...))
}

var DB *pgxpool.Pool
var MinioClient *minio.Client

func InitDB() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://postgres:password@127.0.0.1:5432/miiverse?sslmode=disable"
	}

	var err error
	DB, err = pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		logFatal("Unable to connect to database: %v", err)
	}

	if err := DB.Ping(context.Background()); err != nil {
		logFatal("Database ping failed: %v", err)
	}

	LogSuccess("PostgreSQL Database connected for API!")
}

func InitMinio() {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9000"
	}
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY")
	secretAccessKey := os.Getenv("MINIO_SECRET_KEY")
	useSSL := os.Getenv("MINIO_USE_SSL") == "true"

	var err error
	MinioClient, err = minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		logFatal("Failed to initialize MinIO client: %v", err)
	}
	LogSuccess("MinIO client initialized successfully!")
}

func logFatal(format string, a ...any) {
	fmt.Printf(format+"\n", a...)
	os.Exit(1)
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Nintendo-Parampack, X-Nintendo-Servicetoken")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func APIAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		paramPackHeader := c.GetHeader("X-Nintendo-Parampack")
		serviceToken := c.GetHeader("X-Nintendo-Servicetoken")

		if serviceToken != "" && paramPackHeader != "" {
			serviceToken = strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(serviceToken))
			paramPackHeader = strings.NewReplacer("\r", "", "\n", "", " ", "", "\t", "").Replace(paramPackHeader)

			decodedBytes, err := base64.StdEncoding.DecodeString(paramPackHeader)
			if err == nil {
				decodedStr := string(decodedBytes)
				if len(decodedStr) >= 2 {
					trimmed := decodedStr[1 : len(decodedStr)-1]
					base64Result := strings.Split(trimmed, "\\")

					paramPack := make(map[string]string)
					for i := 0; i < len(base64Result); i += 2 {
						if i+1 < len(base64Result) {
							paramPack[strings.TrimSpace(base64Result[i])] = strings.TrimSpace(base64Result[i+1])
						}
					}

					if platformIDStr, exists := paramPack["platform_id"]; exists {
						if platform, err := strconv.Atoi(platformIDStr); err == nil {
							platformName := "wiiu"
							if platform == 0 {
								platformName = "3ds"
							}
							c.Set("param_pack", paramPack)
							c.Set("platform", platformName)
							c.Set("service_token", serviceToken)
						}
					}
				}
			}
		}

		c.Next()
	}
}

func handleAccountRegistration(c *gin.Context) {
	nnid := strings.TrimSpace(c.PostForm("nnid"))
	password := c.PostForm("password")
	countryStr := c.PostForm("country")
	languageStr := c.PostForm("language")

	if nnid == "" || password == "" {
		c.String(http.StatusBadRequest, "Please fill in all fields.")
		return
	}

	if len(nnid) < 4 || len(nnid) > 16 {
		c.String(http.StatusBadRequest, "PNID must be between 4 and 16 characters.")
		return
	}

	if len(password) < 4 {
		c.String(http.StatusBadRequest, "Password is too short.")
		return
	}

	ctx := c.Request.Context()

	var exists bool
	err := DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE pnid = $1)", nnid).Scan(&exists)
	if err != nil {
		LogError("Error checking PNID existence: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	if exists {
		c.String(http.StatusBadRequest, "This PNID is already taken.")
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		LogError("Password hashing error: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	miiBaseURL := os.Getenv("MII_API_BASE")
	if miiBaseURL == "" {
		miiBaseURL = "https://mii-unsecure.ariankordi.net"
	}
	miiBaseURL = strings.TrimSuffix(miiBaseURL, "/")

	miiApiURL := fmt.Sprintf("%s/mii_data/%s?api_id=1", miiBaseURL, nnid)
	resp, err := http.Get(miiApiURL)
	var pid int64 = 0
	var screenName string = nnid

	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var miiResp struct {
			PID  int64  `json:"pid"`
			Name string `json:"name"`
		}
		if json.NewDecoder(resp.Body).Decode(&miiResp) == nil {
			pid = miiResp.PID
			if miiResp.Name != "" {
				screenName = miiResp.Name
			}
		}
	}

	imgID := uuid.New().String()
	expressions := []string{"normal", "happy", "surprised", "puzzled", "frustrated", "like", "blink", "smile"}
	baseURL := fmt.Sprintf("%s/miis/image.png?nnid=%s&api_id=1", miiBaseURL, nnid)

	cdnBaseURL := os.Getenv("CDN_URL_BASE")
	cdnBaseURL = strings.TrimSuffix(cdnBaseURL, "/")

	uploadedURLs := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, expr := range expressions {
		wg.Add(1)
		go func(expression string) {
			defer wg.Done()
			imgURL := fmt.Sprintf("%s&expression=%s", baseURL, expression)
			imgResp, err := http.Get(imgURL)
			if err != nil || imgResp.StatusCode != http.StatusOK {
				return
			}
			defer imgResp.Body.Close()

			var buf bytes.Buffer
			_, _ = buf.ReadFrom(imgResp.Body)
			fileBytes := buf.Bytes()
			fileName := fmt.Sprintf("%s_%s.png", imgID, expression)

			_, err = MinioClient.PutObject(ctx, "miis", fileName, bytes.NewReader(fileBytes), int64(len(fileBytes)), minio.PutObjectOptions{
				ContentType: "image/png",
			})
			if err != nil {
				return
			}

			mu.Lock()
			uploadedURLs[expression] = fmt.Sprintf("%s/miis/%s", cdnBaseURL, fileName)
			mu.Unlock()
		}(expr)
	}
	wg.Wait()

	miiURL := uploadedURLs["normal"]

	var countryID, languageID, regionID, platformID, utcOffset int
	var tzName string

	if countryStr != "" {
		countryID, _ = strconv.Atoi(countryStr)
	}
	if languageStr != "" {
		languageID, _ = strconv.Atoi(languageStr)
	}

	if paramPackVal, existsPP := c.Get("param_pack"); existsPP {
		if pp, ok := paramPackVal.(map[string]string); ok {
			if countryID == 0 {
				countryID, _ = strconv.Atoi(pp["country_id"])
			}
			if languageID == 0 {
				languageID, _ = strconv.Atoi(pp["language_id"])
			}
			if val, err := strconv.Atoi(pp["region_id"]); err == nil {
				regionID = val
			}
			if val, err := strconv.Atoi(pp["platform_id"]); err == nil {
				platformID = val
			}
			if val, err := strconv.Atoi(pp["utc_offset"]); err == nil {
				utcOffset = val
			}
			tzName = pp["tz_name"]
		}
	}

	serviceToken := c.GetString("service_token")
	platformName := c.GetString("platform")

	var query string
	var args []any

	if platformName == "3ds" && serviceToken != "" {
		query = `INSERT INTO accounts (pnid, password_hash, pid, screen_name, mii_url, service_token_3ds, country_id, language_id, region_id, platform_id, utc_offset, tz_name) 
		         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
		args = []any{nnid, string(hashedPassword), pid, screenName, miiURL, serviceToken, countryID, languageID, regionID, platformID, utcOffset, tzName}
	} else if serviceToken != "" {
		query = `INSERT INTO accounts (pnid, password_hash, pid, screen_name, mii_url, service_token_wiiu, country_id, language_id, region_id, platform_id, utc_offset, tz_name) 
		         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
		args = []any{nnid, string(hashedPassword), pid, screenName, miiURL, serviceToken, countryID, languageID, regionID, platformID, utcOffset, tzName}
	} else {
		query = `INSERT INTO accounts (pnid, password_hash, pid, screen_name, mii_url, country_id, language_id, region_id, platform_id, utc_offset, tz_name) 
		         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
		args = []any{nnid, string(hashedPassword), pid, screenName, miiURL, countryID, languageID, regionID, platformID, utcOffset, tzName}
	}

	_, err = DB.Exec(ctx, query, args...)
	if err != nil {
		LogError("Database account insertion error: %v", err)
		c.Status(http.StatusInternalServerError)
		return
	}

	LogSuccess("API: Account successfully created for PNID: %s (PID: %d)", nnid, pid)
	c.Status(http.StatusOK)
}

func main() {
	_ = godotenv.Load()

	InitDB()
	InitMinio()

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(CORSMiddleware())
	router.Use(APIAuthMiddleware())

	router.GET("/health", func(c *gin.Context) {
		c.String(200, "ok")
	})

	router.POST("/v1/people", handleAccountRegistration)

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "3005"
	}

	LogSuccess("Miiverse API started on port " + port)
	if err := router.Run(":" + port); err != nil {
		logFatal("Failed to run API server: %v", err)
	}
}
