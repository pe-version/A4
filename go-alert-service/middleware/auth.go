package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"iot-alert-service/models"
)

// AuthMiddleware validates Bearer token authentication using a constant-time
// comparison to prevent timing-based token recovery attacks (OWASP A07).
func AuthMiddleware(validToken string) gin.HandlerFunc {
	// Precompute the valid token hash once at startup.
	validHash := sha256.Sum256([]byte(validToken))

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Detail: "Not authenticated",
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Detail: "Invalid authorization format. Use: Bearer <token>",
			})
			return
		}

		// Hash the incoming token so length differences don't leak via
		// comparison timing. Then constant-time compare the hashes.
		incomingHash := sha256.Sum256([]byte(parts[1]))
		if subtle.ConstantTimeCompare(incomingHash[:], validHash[:]) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.ErrorResponse{
				Detail: "Invalid or expired token",
			})
			return
		}

		c.Next()
	}
}
