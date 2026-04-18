package repositories

import (
	"database/sql"
	"fmt"

	"iot-sensor-service/models"
)

// SensorRepository defines the interface for sensor data access.
type SensorRepository interface {
	GetAll() ([]models.Sensor, error)
	GetByID(id string) (*models.Sensor, error)
	Create(sensor *models.SensorCreate) (*models.Sensor, error)
	Update(id string, updates *models.SensorUpdate) (*models.Sensor, error)
	Delete(id string) error
}

// PostgresSensorRepository implements SensorRepository using Postgres.
type PostgresSensorRepository struct {
	db *sql.DB
}

// NewSensorRepository creates a new Postgres-backed sensor repository.
func NewSensorRepository(db *sql.DB) *PostgresSensorRepository {
	return &PostgresSensorRepository{db: db}
}

// GetAll retrieves all sensors from the database.
func (r *PostgresSensorRepository) GetAll() ([]models.Sensor, error) {
	rows, err := r.db.Query(`
		SELECT id, name, type, location, value, unit, status, last_reading, created_at, updated_at
		FROM sensors ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sensors []models.Sensor
	for rows.Next() {
		var s models.Sensor
		err := rows.Scan(&s.ID, &s.Name, &s.Type, &s.Location, &s.Value, &s.Unit, &s.Status, &s.LastReading, &s.CreatedAt, &s.UpdatedAt)
		if err != nil {
			return nil, err
		}
		sensors = append(sensors, s)
	}

	if sensors == nil {
		sensors = []models.Sensor{}
	}

	return sensors, rows.Err()
}

// GetByID retrieves a sensor by its ID.
func (r *PostgresSensorRepository) GetByID(id string) (*models.Sensor, error) {
	var s models.Sensor
	err := r.db.QueryRow(`
		SELECT id, name, type, location, value, unit, status, last_reading, created_at, updated_at
		FROM sensors WHERE id = $1
	`, id).Scan(&s.ID, &s.Name, &s.Type, &s.Location, &s.Value, &s.Unit, &s.Status, &s.LastReading, &s.CreatedAt, &s.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Create inserts a new sensor into the database. ID generation uses a
// Postgres SEQUENCE (sensor_id_seq), which is atomic and race-free under
// concurrent writes — no explicit transaction needed.
func (r *PostgresSensorRepository) Create(sensor *models.SensorCreate) (*models.Sensor, error) {
	if err := sensor.Validate(); err != nil {
		return nil, err
	}

	var nextNum int64
	if err := r.db.QueryRow("SELECT nextval('sensor_id_seq')").Scan(&nextNum); err != nil {
		return nil, err
	}
	newID := fmt.Sprintf("sensor-%03d", nextNum)

	now := models.Now()

	_, err := r.db.Exec(`
		INSERT INTO sensors (id, name, type, location, value, unit, status, last_reading, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, newID, sensor.Name, sensor.Type, sensor.Location, sensor.Value, sensor.Unit, sensor.Status, now, now, now)
	if err != nil {
		return nil, err
	}

	return r.GetByID(newID)
}

// Update modifies an existing sensor.
func (r *PostgresSensorRepository) Update(id string, updates *models.SensorUpdate) (*models.Sensor, error) {
	existing, err := r.GetByID(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}

	if err := updates.Validate(); err != nil {
		return nil, err
	}

	argIdx := 1
	query := "UPDATE sensors SET updated_at = $1, last_reading = $2"
	args := []interface{}{models.Now(), models.Now()}
	argIdx = 3

	if updates.Name != nil {
		query += fmt.Sprintf(", name = $%d", argIdx)
		args = append(args, *updates.Name)
		argIdx++
	}
	if updates.Type != nil {
		query += fmt.Sprintf(", type = $%d", argIdx)
		args = append(args, *updates.Type)
		argIdx++
	}
	if updates.Location != nil {
		query += fmt.Sprintf(", location = $%d", argIdx)
		args = append(args, *updates.Location)
		argIdx++
	}
	if updates.Value != nil {
		query += fmt.Sprintf(", value = $%d", argIdx)
		args = append(args, *updates.Value)
		argIdx++
	}
	if updates.Unit != nil {
		query += fmt.Sprintf(", unit = $%d", argIdx)
		args = append(args, *updates.Unit)
		argIdx++
	}
	if updates.Status != nil {
		query += fmt.Sprintf(", status = $%d", argIdx)
		args = append(args, *updates.Status)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, id)

	_, err = r.db.Exec(query, args...)
	if err != nil {
		return nil, err
	}

	return r.GetByID(id)
}

// Delete removes a sensor from the database.
func (r *PostgresSensorRepository) Delete(id string) error {
	result, err := r.db.Exec("DELETE FROM sensors WHERE id = $1", id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}
