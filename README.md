# Miiverse API Backend

A high-performance backend built in **Go** using **Gin**, **PostgreSQL** (`pgx`), and **MinIO** (S3-compatible object storage) for account management and Mii avatar profile synchronization (3DS / Wii U).

## Prerequisites

Before running the project, make sure you have installed:

* **Go** (version 1.18 or higher recommended)

* **PostgreSQL**

* **MinIO** (or any other S3-compatible storage service configured with a `miis` bucket)

## Configuration

| Environment Variable | Description | Value / Example |
|----------------------|-------------|-----------------|
| `HTTP_PORT` | HTTP Port | `3005` |
| `DATABASE_URL` | PostgreSQL database URL | `postgres://user:password@localhost:5432/db_name?sslmode=disable` |
| `MINIO_ENDPOINT` | MinIO Endpoint | `localhost:9000` |
| `MINIO_ACCESS_KEY` | MinIO access key | `your_access_key` |
| `MINIO_SECRET_KEY` | MinIO secret key | `your_secret_key` |
| `MINIO_USE_SSL` | MinIO SSL (`true` / `false`) | `false` |
| `MII_API_BASE` | Mii API Base URL | `https://mii-unsecure.ariankordi.net` |
| `CDN_URL_BASE` | CDN Base URL | `https://cdn.olv.example.com` |


## Installation & Running

1. **Clone the project or place the code** into a directory.

2. **Initialize Go modules** (if not already done):

   ```
   go mod init api
   go mod tidy
   
   ```

3. **Run the application:**

   ```
   go run main.go
   
   ```

The server will start by default on port **`3005`** (or whatever is defined in `HTTP_PORT`).
