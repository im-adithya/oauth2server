package tokens

type Config struct {
	AccessTokenExpSeconds  int    `envconfig:"ACCESS_EXPIRY_SECONDS" default:"7200"`     //default 2 hours
	RefreshTokenExpSeconds int    `envconfig:"REFRESH_EXPIRY_SECONDS" default:"2592000"` //default 30 days
	LndHubUrl              string `envconfig:"LNDHUB_URL" required:"true"`
	JWTSecret              []byte `envconfig:"JWT_SECRET" required:"true"`
}
