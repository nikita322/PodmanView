# PodmanView Plugin System

Система плагинов позволяет расширять функциональность PodmanView без изменения основного кода.

## Архитектура

Все плагины компилируются в бинарник и могут быть включены/выключены через конфигурацию `.env`.

## Создание плагина

### 1. Создайте директорию для плагина

```bash
mkdir internal/plugins/myplugin
```

### 2. Создайте файл plugin.go

```go
package myplugin

import (
    "context"
    "encoding/json"
    "net/http"

    "podmanview/internal/plugins"
)

type MyPlugin struct {
    *plugins.BasePlugin
    // Добавьте свои поля
}

func New() *MyPlugin {
    return &MyPlugin{
        BasePlugin: plugins.NewBasePlugin(
            "myplugin",              // уникальное имя (lowercase)
            "My awesome plugin",      // описание
            "1.0.0",                 // версия
        ),
    }
}

// Init инициализирует плагин
func (p *MyPlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
    p.SetDependencies(deps)
    p.LogInfo("Initializing my plugin")

    // Читайте настройки из конфигурации
    setting := p.GetPluginSettingOrDefault("MY_SETTING", "default_value")
    p.LogInfo("Setting: %s", setting)

    return nil
}

// Start запускает плагин
func (p *MyPlugin) Start(ctx context.Context) error {
    p.LogInfo("Starting my plugin")
    return nil
}

// Stop останавливает плагин
func (p *MyPlugin) Stop(ctx context.Context) error {
    p.LogInfo("Stopping my plugin")
    return nil
}

// Routes возвращает HTTP маршруты
func (p *MyPlugin) Routes() []plugins.Route {
    return []plugins.Route{
        {
            Method:      "GET",
            Path:        "/api/plugins/myplugin/status",
            Handler:     p.handleStatus,
            RequireAuth: true,
        },
    }
}

// IsEnabled проверяет, включен ли плагин
func (p *MyPlugin) IsEnabled() bool {
    if p.Deps() == nil || p.Deps().Config == nil {
        return false
    }

    enabled := p.Deps().Config.EnabledPlugins()
    for _, name := range enabled {
        if name == p.Name() {
            return true
        }
    }

    return false
}

// HTTP обработчики

func (p *MyPlugin) handleStatus(w http.ResponseWriter, r *http.Request) {
    response := map[string]string{
        "status": "ok",
        "plugin": p.Name(),
    }

    writeJSON(w, http.StatusOK, response)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(data)
}
```

### 3. Зарегистрируйте плагин в main.go

```go
import (
    "podmanview/internal/plugins/myplugin"
)

func main() {
    // ...

    pluginRegistry := plugins.NewRegistry()

    // Регистрируем плагины
    pluginRegistry.Register(demo.New())
    pluginRegistry.Register(myplugin.New())  // <- Добавьте ваш плагин

    // ...
}
```

### 4. Добавьте конфигурацию в .env

```bash
# Включите плагин
PODMANVIEW_PLUGINS_ENABLED=demo,myplugin

# Настройки плагина (опционально)
PLUGIN_MYPLUGIN_MY_SETTING=custom_value
```

## Доступные зависимости

Плагины имеют доступ к:

- **PodmanClient** - клиент для работы с Podman API
- **Config** - конфигурация приложения
- **EventStore** - хранилище событий для логирования
- **Logger** - логгер приложения

## BasePlugin методы

BasePlugin предоставляет вспомогательные методы:

```go
// Логирование
p.LogInfo("Info message: %s", value)
p.LogError("Error: %v", err)

// События
p.AddEvent("event_type", "Event message")

// Настройки
value, ok := p.GetPluginSetting("KEY")
value := p.GetPluginSettingOrDefault("KEY", "default")
```

## HTTP маршруты

Рекомендации:
- Используйте префикс `/api/plugins/{plugin-name}/`
- Все маршруты должны требовать аутентификацию (`RequireAuth: true`)
- Возвращайте JSON

## Примеры

См. примеры в:
- `internal/plugins/demo/` - простой демонстрационный плагин
- `PLUGIN_ARCHITECTURE.md` - детальная архитектура и примеры

## API для управления плагинами

- `GET /api/plugins` - список всех плагинов
- `GET /api/plugins/{name}` - информация о конкретном плагине

## Внешние плагины

Для создания плагина в отдельном репозитории:

1. Создайте отдельный Go модуль
2. Реализуйте интерфейс `plugins.Plugin`
3. Добавьте в `go.mod` основного проекта:
   ```
   require github.com/username/podmanview-plugin-name v1.0.0
   ```
4. Зарегистрируйте в `main.go`:
   ```go
   import customplugin "github.com/username/podmanview-plugin-name"
   pluginRegistry.Register(customplugin.New())
   ```
5. Пересоберите проект

## Best Practices

1. **Имена**: используйте lowercase без пробелов
2. **Версии**: следуйте semver (1.0.0)
3. **Логирование**: используйте методы BasePlugin
4. **Ошибки**: возвращайте информативные ошибки из Init/Start/Stop
5. **Graceful shutdown**: корректно освобождайте ресурсы в Stop()
6. **Безопасность**: валидируйте все входные данные
7. **Документация**: добавьте README в директорию плагина

## Troubleshooting

### Плагин не загружается

- Проверьте, что плагин включен в `PODMANVIEW_PLUGINS_ENABLED`
- Проверьте логи на ошибки инициализации
- Убедитесь, что плагин зарегистрирован в `main.go`

### Маршруты не работают

- Убедитесь, что метод `Routes()` возвращает корректные маршруты
- Проверьте, что путь начинается с `/api/plugins/{plugin-name}/`
- Проверьте логи при старте - должны быть сообщения о регистрации маршрутов

### Настройки не читаются

- Формат: `PLUGIN_{NAME}_{SETTING}` (NAME в UPPERCASE)
- Пример: `PLUGIN_MYPLUGIN_API_KEY=value`
- Используйте `p.GetPluginSetting()` или `p.GetPluginSettingOrDefault()`
