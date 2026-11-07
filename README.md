# Team Task Manager

[![CI](https://github.com/lumen-dev02/team-task-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/lumen-dev02/team-task-manager/actions/workflows/ci.yml)

Учебный backend на Go для совместной работы над задачами. Пользователь создаёт проект, добавляет зарегистрированных участников и назначает им задачи. Проект сделан как основа для курсовой и практики REST API, SQL, транзакций и проверки прав доступа. Это не описание коммерческого продукта или опыта работы.

## Возможности

- Регистрация, вход и JWT access-токен на один час.
- Создание, просмотр и удаление проектов; список только своих проектов.
- Две роли: владелец и участник. Только владелец управляет составом команды и удаляет проект.
- Создание, чтение, полное обновление и удаление задач участниками проекта.
- Статусы `todo`, `in_progress`, `done`; исполнитель из числа участников или `null`.
- Фильтрация задач по статусу и исполнителю, пагинация списков проектов, задач и истории.
- История создания и смены статусов с автором и временем изменения.
- Проверка версии при обновлении задачи: устаревшая правка получает `409 Conflict`.
- При удалении участника его задачи сохраняются, исполнитель снимается, версия увеличивается.

## Стек

Go 1.26+, `net/http`, PostgreSQL 17, `pgx/v5`, `golang-jwt/jwt/v5`, bcrypt. Docker Compose нужен только для приложения и базы данных. Redis, брокера сообщений и ORM нет: для текущих требований они не нужны.

## Быстрый запуск

Нужны Git, Docker с Compose v2 и OpenSSL. Go для запуска через Docker не требуется.

```bash
git clone https://github.com/lumen-dev02/team-task-manager.git
cd team-task-manager
cp .env.example .env
openssl rand -hex 32
```

Вставьте результат последней команды в `JWT_SECRET` в `.env`. Шаблонный секрет приложение намеренно не принимает. Пароль PostgreSQL в примере предназначен только для локальной разработки; при замене используйте URL-safe значение, например hex-строку.

```bash
docker compose up --build -d
docker compose ps -a
curl --fail http://localhost:8080/readyz
```

Compose ждёт готовности БД, запускает отдельный контейнер миграции и затем API. Для `migrate` нормальное состояние — `Exited (0)`. База доступна на `127.0.0.1:55432`, API — на `127.0.0.1:8080`. Порты можно изменить в `.env`.

```bash
docker compose logs api migrate
docker compose down
```

`down` сохраняет данные в Docker volume. Команда `docker compose down -v` **удаляет все локальные данные проекта**; используйте её только для намеренного сброса учебной базы.

### Без Docker для приложения

Нужны Go 1.26+ и запущенный PostgreSQL. Базу можно поднять отдельно:

```bash
docker compose up -d --wait db
export DATABASE_URL='postgres://taskmanager:local-development-only@localhost:55432/taskmanager?sslmode=disable'
export JWT_SECRET="$(openssl rand -hex 32)"
go run ./cmd/api -migrate
go run ./cmd/api
```

Пример URL соответствует исходным значениям `.env.example`. При изменении реквизитов обновите URL. Приложение читает переменные окружения, **не загружает `.env` самостоятельно**. По умолчанию слушает `:8080`; настройка — `HTTP_ADDR`.

## Пример API

```bash
curl --fail-with-body -sS http://localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"timur@example.com","name":"Timur","password":"local-demo-password"}'

curl --fail-with-body -sS http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"timur@example.com","password":"local-demo-password"}'
```

Скопируйте `access_token` из ответа входа:

```bash
export TOKEN='<access_token>'
curl --fail-with-body -sS http://localhost:8080/api/v1/projects \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"Course project","description":"Team planning"}'

# Подставьте id проекта из ответа вместо 1.
curl --fail-with-body -sS http://localhost:8080/api/v1/projects/1/tasks \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"title":"Describe the API"}'
```

Полный повторяемый сценарий с двумя пользователями, назначением исполнителя, сменой статуса и удалением участника:

```bash
# Дополнительно нужны curl, jq и openssl. API должен быть запущен.
bash scripts/demo.sh
# Для другого порта: BASE_URL=http://localhost:8081 bash scripts/demo.sh
```

Сценарий создаёт отдельные тестовые аккаунты и оставляет демонстрационный проект в БД. Токены не выводятся. [Справочник API](docs/api.md) содержит все маршруты, тела запросов, права и ошибки.

## Тесты и проверки

```bash
go test -race -count=1 ./...
go vet ./...
go build ./cmd/api
```

Без `TEST_DATABASE_URL` тесты PostgreSQL явно пропускаются. Чтобы запустить **все** тесты с настоящей БД:

```bash
docker compose up -d --wait db
export TEST_DATABASE_URL='postgres://taskmanager:local-development-only@localhost:55432/taskmanager?sslmode=disable'
go test -race -count=1 ./... -v
# Или только интеграционные:
make test-integration
```

Тестовый пользователь PostgreSQL должен иметь право `CREATE SCHEMA`. Каждый интеграционный тест создаёт изолированную схему и удаляет её после выполнения; таблицы приложения не очищаются. Тем не менее для тестов лучше использовать отдельную БД, не рабочую.

Проверяются JWT и пароли, валидация JSON, конфигурация, авторизация, фильтры, история, миграция и её checksum, конфликт конкурентных обновлений и гонка назначения задачи с удалением участника. CI поднимает PostgreSQL, запускает тесты с race detector, `go vet` и собирает контейнер. Числа покрытия и производительности заранее не заявляются.

## Структура

```text
cmd/api/            точка входа, запуск миграций, graceful shutdown
internal/auth/      bcrypt и JWT
internal/config/    конфигурация из окружения
internal/domain/    модели и ошибки
internal/httpapi/   маршруты, middleware, валидация, HTTP-тесты
internal/store/     SQL, транзакции и проектные права доступа
migrations/        встроенная начальная SQL-миграция и её запуск
docs/              контракт API и архитектурные решения
scripts/           демонстрационный сценарий
```

Отдельного сервисного слоя пока нет: основные правила связаны с доступом к данным и транзакциями и находятся в `store`. Нет интерфейсов ради моков — SQL и права проверяются интеграционными тестами. Подробнее: [архитектура и компромиссы](docs/architecture.md).

## Границы проекта

- Это API без веб-интерфейса, уведомлений, дедлайнов, вложений и поиска.
- Добавление участника по email — немедленное действие владельца, не приглашение с подтверждением. Проверки владения email нет.
- Нет refresh-токенов, отзыва JWT, восстановления пароля и передачи владения проектом. При удалении задач и проектов их история тоже удаляется: это не неизменяемый аудит.
- Лимит регистрации и входа общий на процесс: 2 запроса/секунду, burst 5. Это базовая защита затратного bcrypt, не полноценная защита публичного сервиса.
- Операции одного проекта сериализуются блокировкой строки проекта, включая чтение. Решение простое, но не рассчитано на высокую нагрузку.
- Docker-конфигурация локальная. Для публичного размещения нужны HTTPS, отдельные секреты, ограничения доступа к БД, резервные копии, более точные лимиты и наблюдаемость.

Лицензия: [MIT](LICENSE).
