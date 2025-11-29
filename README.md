# RVPodView

Lightweight web-based Podman management panel for Orange Pi RV2 and other RISC-V single-board computers.

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)
![Podman](https://img.shields.io/badge/Podman-4.0+-892CA0?logo=podman&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green)

## Features

### Container Management
- List all containers (running/stopped/all)
- Create containers from images with port mappings, volumes, and environment variables
- Start/Stop/Restart containers
- Remove containers (with force option)
- View container logs
- Terminal access (exec) via WebSocket
- Inspect container details

### Image Management
- List all images
- Pull images from registry
- Remove images
- Inspect image details

### System
- Dashboard with system statistics
- Host information (OS, kernel, architecture)
- Disk usage overview
- CPU and memory usage
- Temperature monitoring
- System prune (cleanup unused resources)
- Host reboot/shutdown controls
- Auto-refresh toggle (state saved in localStorage)

### PWA Support
- Installable as app on mobile and desktop
- Standalone mode (no browser UI)
- Offline caching for static assets

### Host Terminal
- Full terminal access to the host system
- WebSocket-based with xterm.js
- Admin-only access

### Authentication
- PAM authentication (Linux system users)
- JWT tokens stored in HttpOnly cookies
- Role-based access control:
  - **Admin** (wheel/sudo group): Full access
  - **User**: Read-only access
- 24-hour session lifetime

## Screenshots
<img width="1718" height="1272" alt="image" src="https://github.com/user-attachments/assets/d0c9fdb8-1a49-427c-883b-301bf68c0d37" />

## Requirements

- Go 1.21+
- Podman 4.0+
- Linux with PAM support

## Installation

### Quick Start

```bash
# Clone the repository
git clone https://github.com/yourusername/rvpodview.git
cd rvpodview

# Build
go build -o rvpodview ./cmd/rvpodview

# Run (requires root for PAM and port 80)
sudo ./rvpodview
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

1. Open your browser and navigate to the server IP address
2. Login with your Linux system credentials
3. Users in `wheel` or `sudo` group have full admin access
4. Other users have read-only access

## API Endpoints

### Authentication
- `POST /api/auth/login` - Login
- `POST /api/auth/logout` - Logout
- `GET /api/auth/me` - Current user info

### Containers
- `GET /api/containers` - List containers
- `POST /api/containers` - Create container
- `GET /api/containers/{id}` - Inspect container
- `GET /api/containers/{id}/logs` - Get logs
- `POST /api/containers/{id}/start` - Start
- `POST /api/containers/{id}/stop` - Stop
- `POST /api/containers/{id}/restart` - Restart
- `DELETE /api/containers/{id}` - Remove
- `GET /api/containers/{id}/terminal` - Terminal (WebSocket)

### Images
- `GET /api/images` - List images
- `GET /api/images/{id}` - Inspect image
- `POST /api/images/pull` - Pull image
- `DELETE /api/images/{id}` - Remove image

### System
- `GET /api/system/dashboard` - Dashboard data
- `GET /api/system/info` - System info
- `GET /api/system/df` - Disk usage
- `POST /api/system/prune` - System prune

### Terminal
- `GET /api/terminal` - Host terminal (WebSocket, admin only)

## Tech Stack

- **Backend**: Go with Chi router
- **Frontend**: Vanilla JavaScript, xterm.js
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
│   │   ├── css/        # Styles
│   │   ├── js/         # Frontend JavaScript
│   │   └── img/        # Icons and images
│   └── templates/      # HTML templates
└── README.md
```

## Security Notes

- Always use HTTPS in production (via reverse proxy)
- The `-no-auth` flag should never be used in production
- PAM authentication uses system credentials - use strong passwords
- Admin access is restricted to users in wheel/sudo groups

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
