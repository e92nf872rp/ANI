// ANI PostgreSQL versioned migration configuration.
//
// DATABASE_URL and TEST_DATABASE_URL are supplied by the caller/environment;
// credentials must not be committed to this file.

env "local" {
  url = getenv("DATABASE_URL")

  migration {
    dir = "file://deploy/migrations"
  }
}

env "test" {
  url = "postgresql://ani:NMAN1XtQ@10.10.1.66:30945/ani?sslmode=disable"

  migration {
    dir = "file://deploy/migrations"
  }
}
