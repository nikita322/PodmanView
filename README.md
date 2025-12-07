# PodmanView

Minimalist web interface designed specifically for monitoring and managing Podman containers on RISC-V and ARM systems. Perfect for home labs, development environments, and embedded systems.

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)
![Podman](https://img.shields.io/badge/Podman-4.0+-892CA0?logo=podman&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green)

## Demo

![PodmanView Demo](https://github.com/user-attachments/assets/ca9603ea-1ac6-4410-979a-1f69a440ceff)

## Quick Start

### Requirements

- Linux with PAM support
- Podman 4.0+
- Root access (for PAM authentication and port 80)

### Installation

#### Option 1: Download Pre-built Binary (Recommended)

**For ARM64 (Raspberry Pi 4/5, Orange Pi 5, etc.):**
```bash
wget https://github.com/nikita322/PodmanView/releases/latest/download/podmanview-linux-arm64.tar.gz
tar -xzvf podmanview-linux-arm64.tar.gz
sudo ./podmanview
```

**For RISC-V 64-bit (Orange Pi RV2, VisionFive 2, etc.):**
```bash
wget https://github.com/nikita322/PodmanView/releases/latest/download/podmanview-linux-riscv64.tar.gz
tar -xzvf podmanview-linux-riscv64.tar.gz
sudo ./podmanview
```

#### Option 2: Build from Source

Requires Go 1.21+

```bash
# Clone
git clone https://github.com/nikita322/PodmanView.git
cd podmanview

# Build on RISC-V device
make build

# Or cross-compile for RISC-V from another machine
make build-riscv64

# Run
sudo ./podmanview
```

### Run as Systemd Service

```bash
# Install to /opt/podmanview
sudo mkdir -p /opt/podmanview
sudo cp podmanview /opt/podmanview/
sudo cp -r web /opt/podmanview/

# Create service file
sudo tee /etc/systemd/system/podmanview.service << 'EOF'
[Unit]
Description=PodmanView - Podman Web Management
After=network.target podman.socket

[Service]
Type=simple
WorkingDirectory=/opt/podmanview
ExecStart=/opt/podmanview/podmanview
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable podmanview
sudo systemctl start podmanview
```

### Configuration

PodmanView uses a `.env` file for configuration. On first run, it automatically creates `.env` with default values and generates a secure JWT secret.

#### Configuration File (.env)

```bash
# Server address (host:port)
PODMANVIEW_ADDR=:80

# JWT secret key (auto-generated on first run)
PODMANVIEW_JWT_SECRET=

# JWT token expiration in seconds (default: 24 hours)
PODMANVIEW_JWT_EXPIRATION=86400

# Disable authentication (development only!)
PODMANVIEW_NO_AUTH=false

# Podman socket path (auto-detect if empty)
PODMANVIEW_SOCKET=
```

#### Configuration Behavior

- **First run**: Creates `.env` with defaults and auto-generates JWT secret
- **Subsequent runs**: Loads settings from `.env` file
- **Missing JWT secret**: Auto-generates and saves to file
- **Runtime changes**: Configuration stored in memory, changes update both memory and file
- **System env vars**: Ignored - only `.env` file is used for predictable behavior

See `.env.example` for full documentation of all options.

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
podmanview/
├── cmd/podmanview/      # Application entry point
├── internal/
│   ├── api/            # HTTP handlers
│   ├── auth/           # PAM authentication & JWT
│   ├── config/         # Configuration management (.env)
│   ├── events/         # Event store
│   └── podman/         # Podman client
├── web/
│   ├── static/
│   │   ├── css/        # Styles (dark theme)
│   │   ├── js/         # Frontend JavaScript
│   │   └── img/        # Icons and images
│   └── templates/      # HTML templates
├── .env.example        # Configuration template
├── Makefile            # Build commands
└── README.md
```

## Security Notes

- Always use HTTPS in production (via reverse proxy like nginx)
- `PODMANVIEW_NO_AUTH=true` should never be used in production
- PAM authentication uses system credentials - use strong passwords
- Admin access is restricted to users in wheel/sudo groups
- JWT secret is auto-generated and stored in `.env` - keep this file secure

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
