package repositories

import (
	"database/sql"
	"fmt"

	"iot-alert-service/models"
)

// AlertRuleRepository defines the interface for alert rule data access.
type AlertRuleRepository interface {
	GetAll() ([]models.AlertRule, error)
	GetByID(id string) (*models.AlertRule, error)
	GetActiveRulesForSensor(sensorID string) ([]models.AlertRule, error)
	Create(rule *models.AlertRuleCreate) (*models.AlertRule, error)
	Update(id string, updates *models.AlertRuleUpdate) (*models.AlertRule, error)
	Delete(id string) error
}

// PostgresAlertRuleRepository implements AlertRuleRepository using Postgres.
type PostgresAlertRuleRepository struct {
	db *sql.DB
}

// NewAlertRuleRepository creates a new Postgres-backed alert rule repository.
func NewAlertRuleRepository(db *sql.DB) *PostgresAlertRuleRepository {
	return &PostgresAlertRuleRepository{db: db}
}

// GetAll retrieves all alert rules from the database.
func (r *PostgresAlertRuleRepository) GetAll() ([]models.AlertRule, error) {
	rows, err := r.db.Query(`
		SELECT id, sensor_id, metric, operator, threshold, name, status, created_at, updated_at
		FROM alert_rules ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []models.AlertRule
	for rows.Next() {
		var rule models.AlertRule
		err := rows.Scan(&rule.ID, &rule.SensorID, &rule.Metric, &rule.Operator,
			&rule.Threshold, &rule.Name, &rule.Status, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}

	if rules == nil {
		rules = []models.AlertRule{}
	}

	return rules, rows.Err()
}

// GetByID retrieves an alert rule by its ID.
func (r *PostgresAlertRuleRepository) GetByID(id string) (*models.AlertRule, error) {
	var rule models.AlertRule
	err := r.db.QueryRow(`
		SELECT id, sensor_id, metric, operator, threshold, name, status, created_at, updated_at
		FROM alert_rules WHERE id = $1
	`, id).Scan(&rule.ID, &rule.SensorID, &rule.Metric, &rule.Operator,
		&rule.Threshold, &rule.Name, &rule.Status, &rule.CreatedAt, &rule.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

// GetActiveRulesForSensor retrieves all active rules for a given sensor.
func (r *PostgresAlertRuleRepository) GetActiveRulesForSensor(sensorID string) ([]models.AlertRule, error) {
	rows, err := r.db.Query(`
		SELECT id, sensor_id, metric, operator, threshold, name, status, created_at, updated_at
		FROM alert_rules WHERE sensor_id = $1 AND status = 'active' ORDER BY id
	`, sensorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []models.AlertRule
	for rows.Next() {
		var rule models.AlertRule
		err := rows.Scan(&rule.ID, &rule.SensorID, &rule.Metric, &rule.Operator,
			&rule.Threshold, &rule.Name, &rule.Status, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}

	if rules == nil {
		rules = []models.AlertRule{}
	}

	return rules, rows.Err()
}

// Create inserts a new alert rule into the database.
// Uses a transaction to ensure atomic ID generation and insertion,
// preventing duplicate IDs under concurrent access.
func (r *PostgresAlertRuleRepository) Create(rule *models.AlertRuleCreate) (*models.AlertRule, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}

	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var maxNum sql.NullInt64
	err = tx.QueryRow("SELECT MAX(CAST(SUBSTR(id, 6) AS INTEGER)) FROM alert_rules WHERE id LIKE 'rule-%'").Scan(&maxNum)
	if err != nil {
		return nil, err
	}

	nextNum := int64(1)
	if maxNum.Valid {
		nextNum = maxNum.Int64 + 1
	}
	newID := fmt.Sprintf("rule-%03d", nextNum)

	now := models.Now()

	_, err = tx.Exec(`
		INSERT INTO alert_rules (id, sensor_id, metric, operator, threshold, name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, newID, rule.SensorID, rule.Metric, rule.Operator, rule.Threshold, rule.Name, rule.Status, now, now)

	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return r.GetByID(newID)
}

// Update modifies an existing alert rule.
func (r *PostgresAlertRuleRepository) Update(id string, updates *models.AlertRuleUpdate) (*models.AlertRule, error) {
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

	argIdx := 2
	query := "UPDATE alert_rules SET updated_at = $1"
	args := []interface{}{models.Now()}

	if updates.SensorID != nil {
		query += fmt.Sprintf(", sensor_id = $%d", argIdx)
		args = append(args, *updates.SensorID)
		argIdx++
	}
	if updates.Metric != nil {
		query += fmt.Sprintf(", metric = $%d", argIdx)
		args = append(args, *updates.Metric)
		argIdx++
	}
	if updates.Operator != nil {
		query += fmt.Sprintf(", operator = $%d", argIdx)
		args = append(args, *updates.Operator)
		argIdx++
	}
	if updates.Threshold != nil {
		query += fmt.Sprintf(", threshold = $%d", argIdx)
		args = append(args, *updates.Threshold)
		argIdx++
	}
	if updates.Name != nil {
		query += fmt.Sprintf(", name = $%d", argIdx)
		args = append(args, *updates.Name)
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

// Delete removes an alert rule from the database.
func (r *PostgresAlertRuleRepository) Delete(id string) error {
	result, err := r.db.Exec("DELETE FROM alert_rules WHERE id = $1", id)
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
