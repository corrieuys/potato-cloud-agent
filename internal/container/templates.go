package container

// Dockerfile templates for each language
const (
	// nodejsDockerfile is a single-stage build for Node.js applications
	// Single-stage is used because the benefit of multi-stage is minimal
	// (~25% size reduction) compared to the added complexity
	nodejsDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["npm", "start"]
`

	// golangDockerfile is a multi-stage build for Go applications
	// Multi-stage provides ~90% size reduction (350MB → 25MB)
	// by only including the compiled binary in the final image
	golangDockerfile = `FROM {{.BaseImage}} AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o app

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/app .
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["./app"]
`

	// pythonDockerfile is a single-stage build for Python applications
	// Single-stage is used because Python benefits are minimal
	// and dependencies often need compilation
	pythonDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["python", "app.py"]
`

	// rustDockerfile is a multi-stage build for Rust applications
	// Multi-stage provides ~90% size reduction similar to Go
	// by only including the compiled binary in the final image
	rustDockerfile = `FROM {{.BaseImage}} AS builder
WORKDIR /app
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo "fn main() {}" > src/main.rs
RUN cargo build --release && rm -rf src
COPY . .
RUN cargo build --release

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/target/release/app .
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["./app"]
`

	// javaDockerfile is a single-stage build for Java applications
	// Single-stage is simpler; for JDK to JRE reduction
	// consider using a JRE base image instead of JDK
	javaDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY . .
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["java", "-jar", "app.jar"]
`

	// genericDockerfile is a single-stage build for generic applications
	// This is a fallback template when language cannot be detected
	genericDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY . .
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "while true; do sleep 3600; done"]
`
)
