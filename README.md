# RVPodView

Lightweight web-based Podman management panel for Orange Pi RV2 and other RISC-V single-board computers.

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)
![Podman](https://img.shields.io/badge/Podman-4.0+-892CA0?logo=podman&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green)

## Demo

![RVPodView Demo](https://github.com/user-attachments/assets/ca9603ea-1ac6-4410-979a-1f69a440ceff)

## Quick Start

### Requirements

- Linux with PAM support
- Podman 4.0+
- Root access (for PAM authentication and port 80)

### Installation

#### Option 1: Download Pre-built Binary (Recommended)

```bash
# Download latest release for RISC-V 64-bit
wget https://github.com/nir0k/rvpodview/releases/latest/download/rvpodview-linux-riscv64.tar.gz

# Extract
tar -xzvf rvpodview-linux-riscv64.tar.gz

# Run
sudo ./rvpodview
```

#### Option 2: Build from Source

Requires Go 1.21+

```bash
# Clone
git clone https://github.com/nir0k/rvpodview.git
cd rvpodview

# Build on RISC-V device
make build

# Or cross-compile for RISC-V from another machine
make build-riscv64

# Run
sudo ./rvpodview
```

### Run as Systemd Service

#### Option A: Pre-built binary

```bash
# Copy binary to /usr/local/bin
sudo cp rvpodview /usr/local/bin/

# Create service file
sudo tee /etc/systemd/system/rvpodview.service << 'EOF'
[Unit]
Description=RVPodView - Podman Web Management
After=network.target podman.socket

[Service]
Type=simple
ExecStart=/usr/local/bin/rvpodview -addr :80
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable rvpodview
sudo systemctl start rvpodview
```

#### Option B: Build from source on start

```bash
sudo tee /etc/systemd/system/rvpodview.service << 'EOF'
[Unit]
Description=RVPodView - Podman Web Management
After=network.target podman.socket

[Service]
Type=simple
WorkingDirectory=/root/RVPodView
Environment=GOPATH=/root/go
Environment=GOMODCACHE=/root/go/pkg/mod
Environment=HOME=/root
ExecStartPre=/usr/local/go/bin/go build -o rvpodview ./cmd/rvpodview
ExecStart=/root/RVPodView/rvpodview -addr :80
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable rvpodview
sudo systemctl start rvpodview
```

### Command Line Options

```
-addr string    HTTP server address (default ":80")
-socket string  Podman socket path (auto-detect if empty)
-secret string  JWT secret key (auto-generate if empty)
-no-auth        Disable authentication (development only!)
```

### Environment Variables

- `RVPODVIEW_JWT_SECRET` - JWT secret key

## Usage

1. Open browser: `http://<server-ip>`
2. Login with Linux system credentials
3. Users in `wheel` or `sudo` group get admin access
4. Other users get read-only access

## Features

### Container Management
- List all containers (running/stopped/all)
- Create containers with port mappings, volumes, environment variables
- Start/Stop/Restart/Remove containers
- View container logs (newest first, ANSI codes stripped)
- Terminal access via WebSocket
- Real-time CPU and memory stats

### Image Management
- List images with usage status (In Use / Unused)
- Pull images from registry
- Remove images (force option available)
- Inspect image details

### System Dashboard
- Host information (OS, kernel, architecture)
- Real-time CPU usage (calculated from /proc/stat)
- Memory usage
- Disk usage
- Temperature monitoring (hwmon sensors + NVMe)
- System uptime
- Container/Image/Volume/Network counts

### System Controls (Admin only)
- System prune (cleanup unused resources)
- Host reboot
- Host shutdown

### Host Terminal
- Full terminal access to host system
- WebSocket-based with xterm.js
- Admin-only access

### PWA Support
- Installable as app on mobile and desktop
- Standalone mode (no browser UI)
- Offline caching for static assets

### Authentication
- PAM authentication (Linux system users)
- JWT tokens in HttpOnly cookies
- Role-based access:
  - **Admin** (wheel/sudo group): Full access
  - **User**: Read-only access
- 24-hour session lifetime

## API Endpoints

### Authentication
- `POST /api/auth/login` - Login
- `POST /api/auth/logout` - Logout
- `GET /api/auth/me` - Current user info

### Containers
- `GET /api/containers` - List containers (with stats)
- `POST /api/containers` - Create container
- `GET /api/containers/{id}` - Inspect container
- `GET /api/containers/{id}/logs` - Get logs
- `POST /api/containers/{id}/start` - Start
- `POST /api/containers/{id}/stop` - Stop
- `POST /api/containers/{id}/restart` - Restart
- `DELETE /api/containers/{id}` - Remove
- `GET /api/containers/{id}/terminal` - Terminal (WebSocket)

### Images
- `GET /api/images` - List images (with usage info)
- `GET /api/images/{id}` - Inspect image
- `POST /api/images/pull` - Pull image
- `DELETE /api/images/{id}` - Remove image

### System
- `GET /api/system/dashboard` - Dashboard data
- `GET /api/system/info` - System info
- `GET /api/system/df` - Disk usage
- `POST /api/system/prune` - System prune
- `POST /api/system/reboot` - Reboot host
- `POST /api/system/shutdown` - Shutdown host

### Terminal
- `GET /api/terminal` - Host terminal (WebSocket, admin only)

## Tech Stack

- **Backend**: Go with Chi router
- **Frontend**: Vanilla JavaScript, xterm.js
- **UI**: Dark theme, responsive design
- **Authentication**: PAM + JWT
- **Container Runtime**: Podman REST API via Unix socket

## Project Structure

```
rvpodview/
├── cmd/rvpodview/      # Application entry point
├── internal/
│   ├── api/            # HTTP handlers
│   ├── auth/           # PAM authentication & JWT
│   └── podman/         # Podman client
├── web/
│   ├── static/
│   │   ├── css/        # Styles (dark theme)
│   │   ├── js/         # Frontend JavaScript
│   │   └── img/        # Icons and images
│   └── templates/      # HTML templates
├── Makefile            # Build commands
└── README.md
```

## Security Notes

- Always use HTTPS in production (via reverse proxy like nginx)
- The `-no-auth` flag should never be used in production
- PAM authentication uses system credentials - use strong passwords
- Admin access is restricted to users in wheel/sudo groups

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
