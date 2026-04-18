package repositories

import (
	"database/sql"
	"fmt"

	"iot-alert-service/models"
)

// TriggeredAlertRepository defines the interface for triggered alert data access.
type TriggeredAlertRepository interface {
	GetAll() ([]models.TriggeredAlert, error)
	GetByID(id string) (*models.TriggeredAlert, error)
	Create(ruleID, sensorID string, sensorValue, threshold float64, message string) (*models.TriggeredAlert, error)
	UpdateStatus(id string, status string) (*models.TriggeredAlert, error)
}

// PostgresTriggeredAlertRepository implements TriggeredAlertRepository using Postgres.
type PostgresTriggeredAlertRepository struct {
	db *sql.DB
}

// NewTriggeredAlertRepository creates a new Postgres-backed triggered alert repository.
func NewTriggeredAlertRepository(db *sql.DB) *PostgresTriggeredAlertRepository {
	return &PostgresTriggeredAlertRepository{db: db}
}

// GetAll retrieves all triggered alerts from the database.
func (r *PostgresTriggeredAlertRepository) GetAll() ([]models.TriggeredAlert, error) {
	rows, err := r.db.Query(`
		SELECT id, rule_id, sensor_id, sensor_value, threshold, message, status, created_at, resolved_at
		FROM triggered_alerts ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []models.TriggeredAlert
	for rows.Next() {
		var alert models.TriggeredAlert
		err := rows.Scan(&alert.ID, &alert.RuleID, &alert.SensorID, &alert.SensorValue,
			&alert.Threshold, &alert.Message, &alert.Status, &alert.CreatedAt, &alert.ResolvedAt)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, alert)
	}

	if alerts == nil {
		alerts = []models.TriggeredAlert{}
	}

	return alerts, rows.Err()
}

// GetByID retrieves a triggered alert by its ID.
func (r *PostgresTriggeredAlertRepository) GetByID(id string) (*models.TriggeredAlert, error) {
	var alert models.TriggeredAlert
	err := r.db.QueryRow(`
		SELECT id, rule_id, sensor_id, sensor_value, threshold, message, status, created_at, resolved_at
		FROM triggered_alerts WHERE id = $1
	`, id).Scan(&alert.ID, &alert.RuleID, &alert.SensorID, &alert.SensorValue,
		&alert.Threshold, &alert.Message, &alert.Status, &alert.CreatedAt, &alert.ResolvedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &alert, nil
}

// Create inserts a new triggered alert into the database. ID generation uses
// a Postgres SEQUENCE (alert_id_seq), which is atomic and race-free under
// concurrent writes — no explicit transaction needed.
func (r *PostgresTriggeredAlertRepository) Create(ruleID, sensorID string, sensorValue, threshold float64, message string) (*models.TriggeredAlert, error) {
	var nextNum int64
	if err := r.db.QueryRow("SELECT nextval('alert_id_seq')").Scan(&nextNum); err != nil {
		return nil, err
	}
	newID := fmt.Sprintf("alert-%03d", nextNum)

	now := models.Now()

	_, err := r.db.Exec(`
		INSERT INTO triggered_alerts (id, rule_id, sensor_id, sensor_value, threshold, message, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, newID, ruleID, sensorID, sensorValue, threshold, message, "open", now)
	if err != nil {
		return nil, err
	}

	return r.GetByID(newID)
}

// UpdateStatus updates the status of a triggered alert.
func (r *PostgresTriggeredAlertRepository) UpdateStatus(id string, status string) (*models.TriggeredAlert, error) {
	existing, err := r.GetByID(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}

	var resolvedAt *string
	if status == "resolved" {
		now := models.Now()
		resolvedAt = &now
	}

	_, err = r.db.Exec(
		"UPDATE triggered_alerts SET status = $1, resolved_at = $2 WHERE id = $3",
		status, resolvedAt, id,
	)
	if err != nil {
		return nil, err
	}

	return r.GetByID(id)
}
