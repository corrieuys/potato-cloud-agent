package container

// Dockerfile templates for each language
const (
	// bunDockerfile is a single-stage build for Bun applications
	bunDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY . .
RUN {{.BuildCommand}}
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "{{.RunCommand}}"]
`

	// nodejsDockerfile is a single-stage build for Node.js applications
	nodejsDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN {{.BuildCommand}}
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "{{.RunCommand}}"]
`

	// golangDockerfile is a multi-stage build for Go applications
	golangDockerfile = `FROM {{.BaseImage}} AS builder
WORKDIR /app
COPY . .
RUN {{.BuildCommand}}

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app /app
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "{{.RunCommand}}"]
`

	// pythonDockerfile is a single-stage build for Python applications
	pythonDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
RUN {{.BuildCommand}}
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "{{.RunCommand}}"]
`

	// rustDockerfile is a multi-stage build for Rust applications
	rustDockerfile = `FROM {{.BaseImage}} AS builder
WORKDIR /app
COPY . .
RUN {{.BuildCommand}}

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app /app
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "{{.RunCommand}}"]
`

	// javaDockerfile is a single-stage build for Java applications
	javaDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY . .
RUN {{.BuildCommand}}
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "{{.RunCommand}}"]
`

	// genericDockerfile is a single-stage build for generic applications
	genericDockerfile = `FROM {{.BaseImage}}
WORKDIR /app
COPY . .
RUN {{.BuildCommand}}
ENV PORT={{.Port}}
{{- range $key, $value := .EnvVars }}
ENV {{$key}}={{$value}}
{{- end }}
EXPOSE {{.Port}}
USER 1000:1000
CMD ["sh", "-c", "{{.RunCommand}}"]
`
)
