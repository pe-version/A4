package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CorrelationIDHeader is the HTTP header name for the correlation ID.
const CorrelationIDHeader = "X-Correlation-ID"

// LoggingMiddleware adds correlation IDs and logs requests with structured logging.
func LoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get or generate correlation ID
		correlationID := c.GetHeader(CorrelationIDHeader)
		if correlationID == "" {
			correlationID = uuid.New().String()
		}

		// Record start time
		start := time.Now()

		// Process request
		c.Next()

		// Log request completion
		slog.Info("Request completed",
			"correlation_id", correlationID,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)

		// Add correlation ID to response header
		c.Header(CorrelationIDHeader, correlationID)
	}
}
