package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware validates Bearer token authentication using a constant-time
// comparison to prevent timing-based token recovery attacks (OWASP A07).
func AuthMiddleware(validToken string) gin.HandlerFunc {
	// Precompute the valid token hash once at startup so per-request work is
	// limited to hashing the incoming token and a constant-time compare.
	validHash := sha256.Sum256([]byte(validToken))

	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"detail": "Not authenticated",
			})
			return
		}

		// Parse "Bearer <token>" format
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"detail": "Invalid authorization format. Use: Bearer <token>",
			})
			return
		}

		// Hash the incoming token so the length difference between a wrong
		// token and the valid token cannot leak via comparison timing. Then
		// constant-time compare the two 32-byte hashes.
		incomingHash := sha256.Sum256([]byte(parts[1]))
		if subtle.ConstantTimeCompare(incomingHash[:], validHash[:]) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"detail": "Invalid or expired token",
			})
			return
		}

		// Token is valid, continue to next handler
		c.Next()
	}
}
