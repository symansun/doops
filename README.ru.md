<p align="right"><a href="README.md">中文</a> | <a href="README.en.md">English</a> | <b>Русский</b></p>

# DoOps

<p align="center">
  <img src="docs/images/slogan-en.svg" alt="Управляйте любой машиной — без SSH" width="760">
</p>

> Проект распространяется по лицензии [MIT License](LICENSE).

**Основные участники: 孙勇 (Sūn Yǒng), 高家祺 (Gāo Jiāqí), 宋晨宇 (Sòng Chényǔ).** Порядок указания авторов см. в разделе [Contributors](#contributors).

**Всё ещё платите за SSH-порты, jump-хосты, раздачу ключей, NAT и изоляцию внутренних сетей?** `doops` убирает всё это:

- **Одна команда — прямой путь к машине во внутренней сети**: agent (агент) сам устанавливает постоянное соединение с публичным gateway (шлюзом); публичный IP, VPN, jump-хосты и бастионы не нужны
- **Нулевое бремя SSH-активов**: больше не нужно вести списки SSH-пользователей, портов, паролей и ключей; переподключение после обрыва связи и изоляция внутренней сети перестают быть препятствием
- **Создан для агентов и автоматизации**: CLI, консоль и скиллы (skills) из разных областей используют один и тот же аудируемый путь выполнения, поэтому ИИ может безопасно диагностировать сбои, проводить инспекции и развёртывания — вместо ручного копипаста
- **Безопасность по умолчанию**: единая аутентификация, авторизация по target (цели)/action, сквозной аудит; опасные операции по умолчанию отклоняются

Диагностика сбоев, инспекции, развёртывание, синхронизация файлов — все удалённые операции собраны в одну точку входа: каждый шаг фиксируется, каждое выполнение можно перепроверить.

## Предпросмотр интерфейса

![Главная страница DoOps — инструмент интеллектуальной эксплуатации](docs/images/doops-home.jpg)

![Обзор консоли DoOps Console](docs/images/doops-console.png)

DoOps — единая точка входа: интеллектуальные агенты, консоль, CLI и скиллы из разных областей объединены в один инструментарий эксплуатации, охватывающий серверы, Kubernetes, сайты, базы данных, файлы и инспекции, — а повседневная эксплуатация идёт под единой аутентификацией, авторизацией и аудитом.

## Структура каталогов

| Путь | Описание |
| :--- | :--- |
| `agent/` | Исходный код edge-исполнителя `doops-agent` (Go) |
| `gateway/` | Исходный код публичной control plane `doops-gateway` (Go) |
| `skills/` | Исходный код doops CLI, предсобранные бинарники и скиллы эксплуатации |
| `scripts/` | Скрипты установки, сборки и публикации |
| `docs/` | Документация по протоколу, API, развёртыванию и эксплуатации |
| `test/` | Тесты и приёмочные сценарии |
| `Dockerfile*` | Точки входа сборки базового образа и образа лёгкого обновления |

Этот репозиторий не содержит и не публикует бинарник `do-agent`; для запуска `doops ask` вам нужно самостоятельно предоставить образ, содержащий `do-agent` (см. раздел «Базовый образ и сборка» ниже).

## Архитектурная схема

<p align="center">
  <img src="docs/images/architecture-en.svg" alt="Архитектурная схема DoOps" width="1100">
</p>

В системе есть два ортогональных измерения:

- Режим подключения: CLI/skill подключаются к gateway, а agent внутренней сети сам регистрируется на gateway
- Режим выполнения: быстрый путь `exec/read/write/push/pull/info/check/clean`, медленный путь `ask` / последующий `chat`

Распределение ролей компонентов:

- Клиент: `doops` CLI + doops skill
- Публичная control plane: `gateway/` собирается в `doops-gateway` и отвечает за RBAC, аудит, очереди и маршрутизацию к agent'ам внутренней сети
- Edge-исполнитель: `agent/` собирается в `doops-agent` и отвечает за exec, push/pull, файловые и ИИ-задачи на целевой машине
- Граница безопасности исполнения: `doops-agent` по умолчанию запускает только `doops-agent`, `do-agent` и `buildkitd`; SSH и WebIDE по умолчанию не запускаются
- Способ подключения: CLI/skill обращаются к gateway на порт `42222`, а gateway попадает к agent'у внутренней сети через уже установленный agent WebSocket
- Аутентификация: пользователь подключается к gateway по gateway user token (токену пользователя gateway); для регистрации agent на gateway токен не нужен — достаточно `gateway-url`, `cluster` и `instance`
- Синхронизация кода: gateway использует Git HTTP; gateway прозрачно проксирует Git-запросы только через обратный туннель по уже установленному agent WebSocket и больше не использует загрузку tar-чанками как путь `push/pull`
- Сборка образов: единообразно через `buildctl + buildkitd`

## Соглашение об именовании target

`cluster/instance` (кластер/инстанс) — ключ маршрутизации gateway; имена определяются вашим собственным развёртыванием. Примеры:

- `prod / master-1`
- `staging / worker-1`
- `edge / gpu-node-1`

В любой момент времени на один `cluster/instance` онлайн должен быть только один agent; повторная регистрация заменяет старое соединение.

## Протокол и эндпоинты

- От CLI к gateway: `WS /v1/rpc?cluster=<cluster>&instance=<instance>`
- От agent внутренней сети к gateway: `WS /v1/agent/connect?cluster=<cluster>&instance=<instance>`
- Синхронизация кода: `Git HTTP /v1/git/<cluster>/<instance>/<session>.git`
- Проверка здоровья: `GET /health`
- Адрес OpenAI-совместимого сервиса моделей задаётся через `API_BASE_URL` (пример по умолчанию — `https://llm.example.com/v1`)

## Базовый образ и сборка

Образ agent состоит из двух слоёв:

| Образ | Dockerfile | Содержимое | Частота изменений |
| :--- | :--- | :--- | :--- |
| Базовый образ | `Dockerfile.base` / `Dockerfile.base.light` | sandbox, do-agent, BuildKit, системные утилиты | Низкая |
| Образ лёгкого обновления | `Dockerfile` / `agent/Dockerfile` | `doops-agent`, skills, docs, entrypoint | Высокая |

Перед сборкой необходимо подготовить:

- `DO_AGENT_IMAGE`: образ, содержащий `/usr/local/bin/do-agent`. Этот репозиторий не поставляется с `do-agent` — предоставьте его самостоятельно.
- `DOOPS_AGENT_BASE_IMAGE`: тег базового образа, по умолчанию `doops-base:latest`.

```bash
# 1. 构建基础镜像（推荐 base-light，纯净 agent 运行时）
docker build -f Dockerfile.base.light \
  --build-arg DO_AGENT_IMAGE=<your-doagent-image> \
  -t doops-base:latest .

# 2. 构建轻更新镜像
docker build \
  --build-arg DOOPS_AGENT_BASE_IMAGE=doops-base:latest \
  -t <your-registry>/doops:dev .
```

Для повседневных обновлений достаточно заменить образ лёгкого обновления; базовый образ пересобирается только при изменении sandbox, do-agent, BuildKit или системных пакетов.

## Точка входа кода Gateway

`doops-gateway` — первоклассный компонент; точка входа исходного кода находится в корневом каталоге репозитория:

```bash
bash scripts/build-gateway.sh
./bin/doops-gateway serve -db /var/lib/doops-gateway/gateway.db -port 42222
```

Совместимая точка входа `agent/cmd/gateway` по-прежнему существует, но служит лишь обёрткой для старого пути сборки; новая документация и новые развёртывания должны использовать `gateway/`.

Руководство по использованию API для внешних систем см. в [docs/DOOPS_API_GUIDE.md](docs/DOOPS_API_GUIDE.md): вход, запрос целей, WebSocket JSON-RPC, развёртывание на естественном языке, поток прогресса и запросы к аудиту.

## Проверка после запуска

После развёртывания проверяйте цепочку в следующем порядке:

```bash
doops list
doops targets --target <gateway-target>
doops -session smoke exec --target <gateway-target> --cmd 'hostname'
doops -session smoke push --target <gateway-target> --src <small-dir>
doops -session smoke ask --target <gateway-target> --msg '<read-only task>'
```

Только если `targets` показывает `cluster/instance` онлайн, это значит, что agent зарегистрирован на gateway; если `ask` завершается ошибкой, сначала проверьте, что в `settings.json` внутри `doagent-config` заданы действительные API Key и `API_BASE_URL`.

## Фиксированные пути

- CLI: `~/.local/bin/doops`
- Авторитетное расположение исходника skill: `skills/SKILL.md`
- Цель установки skill: `<project>/.agent/skills/doops/SKILL.md`
- Единственная конфигурация CLI по умолчанию: `~/.agent/skills/doops/config.json`
- Кэш токена CLI: `~/.agent/skills/doops/auth.json`
- Предсобранный CLI: `skills/doops-cli/bin/doops-<os>-<arch>`
- Agent local token: на сервере `/root/.doops/agent-token`; нужен только для устаревшего прямого подключения или защиты локального `/ws` и не используется для регистрации на gateway

`~/.config/doops/config.json`, `~/.doops/auth.json` и проектный `.doops/` — это старые пути или следы прошлых запусков; на них нельзя опираться как на текущую конфигурацию. `DOOPS_CONFIG=/path/to/config.json` предназначен только для тестов и аварийного явного переопределения.

## Модель установки — сначала сюда

В DoOps легко спутать две вещи, поэтому в документации их нужно сразу разводить:

1. **Форма развёртывания agent**: как `doops-agent` запускается на целевой машине.
2. **Режим подключения**: как CLI/skill подключаются к этому agent.

### Формы развёртывания Agent

| Форма | Как распознать | Где применять | Контейнеризована |
| :--- | :--- | :--- | :--- |
| K8s/DaemonSet | `kubectl get pod -A -l app=doops-agent` показывает Pod | Узлы кластера K8s/K3s; рекомендуемая стандартная установка | Да |
| Контейнер Docker/nerdctl | В `docker ps` или `nerdctl ps` есть контейнер с именем `doops-agent` | Одиночные машины или узлы без DaemonSet; рекомендуемая стандартная установка | Да |
| Голый бинарник/systemd | `ps -ef | grep doops-agent` показывает `/usr/local/bin/doops-agent -port ...`, но ни Pod, ни контейнера нет | Временный бутстрап, совместимость со старыми узлами | Нет |

Например, если на машине видно:

```text
/usr/local/bin/doops-agent -port 42222 ...
```

и при этом `kubectl get pod -A -l app=doops-agent` не показывает Pod, а в `docker ps` нет контейнера `doops-agent`, значит, это **прямая установка голым бинарником**, а не установка образом K8s/K3s. Голый бинарник автоматически не получает doagent, BuildKit, skills и способность к самовосстановлению из контейнерного образа; в продуктивной среде рекомендуется перейти на K8s/DaemonSet или контейнерный режим.

### Подключение к Gateway

Стандартный target настраивается только на gateway:

| Куда подключается CLI/skill | Как должен работать целевой agent |
| :--- | :--- |
| `ws(s)://<gateway>/v1/rpc` | Agent внутренней сети сам подключается к публичному gateway с `-gateway-url ... -cluster ... -instance ...` |

Возможность «управлять узлом» зависит от того, зарегистрировался ли этот agent на gateway. Если `doops targets --target <gateway-target>` показывает соответствующий `cluster/instance` онлайн, последующие `exec/ask/push/pull` пройдут успешно. Если узел — это просто локальный бинарник без `-gateway-url`, он не появится на gateway автоматически.

## Установка и подключение клиента

### 1. Установка CLI и skill

Авторитетный исходный код doops skill находится в этом репозитории — `skills/SKILL.md`. Не пишите skill вручную в пользовательских проектах и не поддерживайте его обратным образом; установочный скрипт лишь копирует `skills/SKILL.md` из репозитория в `.agent/skills/doops/SKILL.md` рабочего проекта.

```bash
git clone https://github.com/symansun/doops.git
cd doops
bash scripts/install.sh --project /absolute/path/to/your/project
```

Этот шаг делает следующее:

1. Копирует предсобранный CLI для вашей платформы из `skills/doops-cli/bin/doops-<os>-<arch>` этого репозитория в `~/.local/bin/doops`
2. Устанавливает `skills/SKILL.md` из этого репозитория в `/absolute/path/to/your/project/.agent/skills/doops/SKILL.md`

Если `~/.local/bin` ещё не в `PATH`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

При установке можно сразу записать конфигурацию gateway target.

```bash
bash scripts/install.sh \
  --project /absolute/path/to/your/project \
  --target-name prod \
  --target-gateway https://gateway.example.com \
  --target-cluster prod \
  --target-instance master-1 \
  --target-aliases p \
  --target-token '<GATEWAY_USER_TOKEN>' \
  --use 'prod via gateway'
```

Skill синхронизируется в каталог проекта вместе с установкой. Конфигурация стандартного target обязана содержать `gateway`/`cluster`/`instance`; `token` — это gateway user token.

- target поддерживает короткие имена и псевдонимы: например, основное имя `prod` и псевдоним `p`. Последующая команда `doops -session smoke exec --target p --cmd hostname` разрешится в тот же `prod/master-1`.

## Обслуживание пользователей Gateway

Пользователи, пароли, токены, авторизация и аудит централизованно обслуживаются на gateway и не хранятся в рабочих проектах.

На уровне продукта наружу выставлен только `doops` как повседневная точка входа. Выдача учётных записей и токенов, grant прав, запросы к аудиту и его очистка — административные возможности, которые должны быть сведены к управляющей точке входа `doops admin ...`; рабочие проекты и skill не должны подталкивать пользователя напрямую вызывать внутренние служебные команды gateway.

Модель авторизации:

- Обычный user token по умолчанию даёт все возможности работы с target; scope, которому в старых базах был выдан только `targets:list`, для совместимости тоже трактуется как полноценный target access — чтобы не получалось «видит, но управлять не может». Когда нужна изоляция, явно сужайте `cluster/instance/action`.
- Глобальному администратору право `admin` нужно выдать явно.
- Записи аудита отслеживаются по `user_id`, `token_id`, `cluster`, `instance`, `action`, `session`, `status`.

Диагностика сбоев:

- `401 Unauthorized`: токен недействителен или истёк — аутентификация не пройдена
- `403 Forbidden`: токен действителен, но у пользователя после явного сужения нет прав на текущий `cluster/instance/action` либо нет права администратора gateway `admin`

### 2. Добавление цели внутреннего кластера через gateway

Администратор обслуживает на gateway пользователей, user token'ы и авторизацию. Для регистрации agent на gateway предварительно выписанный токен не нужен; конфигурация на стороне бизнеса только потребляет выданные администратором user token'ы и не ведёт базу данных gateway напрямую.

gateway поддерживает вход по имени пользователя и паролю: сначала обменяйте их на user token, затем работайте с токеном:

```bash
doops login --target prod-master --gateway https://gateway.example.com --username alice
```

Agent внутренней сети сам подключается к публичному gateway:

```bash
doops-agent \
  -gateway-url https://gateway.example.com \
  -cluster prod \
  -instance master-1 \
```

Настройка gateway target на клиенте:

```bash
doops add \
  --name prod-master \
  --gateway https://gateway.example.com \
  --cluster prod \
  --instance master-1 \
  --aliases prod,master \
  --token '<USER_TOKEN>' \
  --use 'prod cluster via gateway'
```

doops поддерживает gateway по `http` / `https`; `http` автоматически преобразуется в `ws` для WebSocket-туннеля.
Для публичной продуктивной среды рекомендуется `https` / `wss`, но CLI и agent не откажутся подключаться, если gateway работает по HTTP.

Проверка:

```bash
doops targets --target prod-master
doops -session smoke exec --target prod-master --cmd 'hostname'
```

### 3. Проверка подключения

```bash
doops list
doops targets --target prod-master
doops -session smoke exec --target prod-master --cmd 'hostname'
doops -session smoke ask --target prod-master --msg '用一句话说明当前机器状态'
```

## Установка Agent на сервере

Если `doops-agent` уже запущен на целевой машине и зарегистрирован на gateway, клиенту достаточно настроить gateway target и gateway user token — SSH не нужен.

SSH используется только для первоначальной установки agent на машину либо для самовосстановления, когда agent полностью недоступен; это не повседневный способ подключения.

### Перед установкой определите текущую форму

```bash
kubectl get pod -A -l app=doops-agent -o wide 2>/dev/null || true
docker ps --format '{{.Names}} {{.Image}} {{.Ports}}' 2>/dev/null | grep doops-agent || true
nerdctl ps --format '{{.Names}} {{.Image}} {{.Ports}}' 2>/dev/null | grep doops-agent || true
ps -ef | grep '[d]oops-agent' || true
ss -lntp | grep ':42222' || true
```

Правила определения:

- Есть Pod: управлять как K8s/DaemonSet.
- Есть контейнер с именем `doops-agent`: управлять как контейнерным режимом.
- Есть только процесс `/usr/local/bin/doops-agent -port ...`: это прямая установка голым бинарником, а не контейнерная установка.
- Порт слушается, но WebSocket отвечает EOF: обычно это старый agent, несовпадение параметров запуска или порт 42222 занят не тем процессом.

### Способ A: стандартная установка K8s/DaemonSet

Подходит для используемой этим репозиторием по умолчанию раскладки K3s/containerd.

```bash
kubectl create namespace doops-system --dry-run=client -o yaml | kubectl apply -f -
kubectl -n doops-system create secret generic registry-credentials \
  --from-literal=username="$REGISTRY_USER" \
  --from-literal=password="$REGISTRY_PASS" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n doops-system apply -f agent/agent-config.yaml
kubectl -n doops-system apply -f agent/agent.yaml
```

### Способ B: стандартная установка контейнером Docker/nerdctl

Подходит для узлов без DaemonSet, где на хосте можно запускать контейнеры. Имя контейнера обязано быть стандартным `doops-agent`.

```bash
docker run -d --name doops-agent \
  --privileged --pid=host --network=host \
  --restart=unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /root/.kube:/root/.kube:ro \
  -v /etc/kubernetes:/etc/kubernetes:ro \
  -e DO_AGENT_MODEL='openai/gpt-5.4' \
  <your-registry>/doops:<release> \
  -port 42222 \
  -gateway-url https://gateway.example.com \
  -cluster prod \
  -instance master-1 \
```

Если в целевой среде используется containerd/nerdctl, замените `docker run` на `nerdctl run`, оставив параметры без изменений. После запуска проверьте:

```bash
docker ps --filter name=doops-agent
docker logs --tail=100 doops-agent
curl -fsS http://127.0.0.1:42222/health
```

### Способ C: разовый бутстрап голого бинарника по SSH

Используется, только когда на целевой машине ещё нет `doops-agent` и нужно поднять голый процесс через SSH-бутстрап:

```bash
doops install \
  --name prod-node \
  --ip 192.0.2.20 \
  --ssh-user root \
  --ssh-password '<SSH_PASSWORD>' \
  --ssh-port 22 \
  --agent-token '<OPTIONAL_AGENT_TOKEN>'
```

`--agent-token` служит только для защиты локального `/ws` у устаревших голых процессов и не является учётными данными для регистрации на gateway. После бутстрапа всё равно нужно подключиться к gateway с `-gateway-url ... -cluster ... -instance ...` и настроить gateway target на клиенте.

Внимание: в настоящее время `doops install` — это точка входа для SSH bootstrap / совместимости; agent может быть поднят как голый бинарник или как контейнер — в зависимости от возможностей рантайма целевой машины и переданных параметров. Для стандартизации в продуктивной среде предпочтительны **Способ A (K8s/DaemonSet)** или **Способ B (контейнерный режим)**.

Начиная с текущей версии, `doops install` больше не считает успешной установкой сам факт «процесс запущен». После установки он сам подключается обратно к свежеустановленному agent и выполняет самопроверку возможностей, как минимум проверяя:

- сам agent отвечает
- виден контейнерный рантайм (`nerdctl` / `docker` / `podman`)
- видны `kubectl` или kubeconfig
- видны `buildctl` и `/run/buildkit/buildkitd.sock`

Проверки «agent отвечает» и «контейнерный рантайм» обязательны: при их провале установка сразу считается неудачной; для `kubectl` и `buildkit` явно выводится `OK` или `MISSING`, чтобы наполовину рабочая среда не была ошибочно признана «готовой».

### Способ D: релиз `doops-agent` мейнтейнером

`deploy.sh` — точка входа релиза для мейнтейнера: собирает образ agent из этого репозитория, пушит его и раскатывает обновление на кластер:

```bash
DOOPS_IMAGE=<your-registry>/doops:<release> bash deploy.sh prod-node
```

`DOOPS_IMAGE` задаёт целевой образ релиза; если переменная не задана, используется placeholder-адрес, который нужно заменить самостоятельно. Релиз в первую очередь выполняется через уже существующее doops-подключение. Только когда целевой agent недоступен, из конфигурации читаются `ssh_user`, `ssh_port`, `ssh_password` для разового самовосстановления по SSH; учётные данные подключения к agent — всегда `token`.

## Частые команды

```bash
doops list
doops -session demo exec --target prod-node --cmd 'uptime && df -h'
doops -session demo ask --target prod-node --msg '检查磁盘和内存压力，给出结论'
doops -session demo push --target prod-node --src .
doops -session demo pull --target prod-node --dest ./demo-output
doops -session demo write --target prod-node --path /root/ws/demo/pod.yaml --file ./pod.yaml
doops -session demo exec --target prod-node --cmd 'ls -la /root/ws/demo'
```

Пояснения к частым командам:

| Команда | Описание |
| :--- | :--- |
| `doops add --gateway --cluster --instance --token` | Добавить gateway target |
| `doops list` | Показать настроенные на клиенте узлы |
| `doops exec` | Выполнить детерминированную команду внутри контейнера удалённого agent |
| `doops ask` | Передать задачу удалённому doagent на самостоятельный анализ и выполнение |
| `doops push` | Синхронизировать локальный код в `/root/ws/$SESSION` с учётом `.gitignore`/`.doopsignore` |
| `doops pull` | Забрать удалённую рабочую область `/root/ws/$SESSION` на локальную машину через Git |
| `doops read` | Просмотр небольших текстовых файлов на удалённой стороне; не предназначен для скачивания больших или бинарных файлов |
| `doops write` | Запись небольших текстовых файлов на удалённой стороне; поддерживаются `--content`, `--file` и stdin |
| `doops clean` | Очистить удалённую рабочую область |
| `doops upgrade` | Выдать обновление образа онлайн-агентам через gateway |

### Массовое обновление Agent через Gateway

Пользователь должен иметь права `agent:upgrade` или `admin`. Рекомендуется сначала выполнить dry-run, а затем раскатывать обновление по кластеру:

```bash
doops -session upgrade_demo upgrade \
  --target prod \
  --cluster prod \
  --image <your-registry>/doops:<release> \
  --mode k8s \
  --namespace doops-system \
  --workload daemonset/doops-agent \
  --container doops-agent \
  --dry-run
```

После удаления `--dry-run` обновление будет выдано на подходящие онлайн `cluster/instance`. В режиме K8s/DaemonSet выполняются `kubectl set image` и `rollout status`; agent'ы — голые бинарники — явно отклоняют обновление образа.

## Примечания для мейнтейнеров

Пересборка и коммит мультиплатформенного CLI:

```bash
bash scripts/build-cli.sh --all
ls -lh skills/doops-cli/bin/
```

Предсобранные артефакты постоянно хранятся в коммитах в `skills/doops-cli/bin/`; установочный скрипт копирует их отсюда в `~/.local/bin/doops`.

## Границы

- Повседневные подключения `doops` не зависят от SSH.
- `token` в конфигурации стандартного target означает только gateway user token.
- Для регистрации agent на gateway токен не нужен; `doops-agent token` используется только для устаревшего прямого подключения или защиты локального `/ws`.
- `ssh_password` сохранён только как параметр SSH bootstrap.
- Подкоманда `bash` остаётся устаревшей точкой входа интерактивного SSH; рекомендуется использовать `doops exec` и `doops ask`.

## Предохранитель команд Agent

Перед выполнением `exec` на стороне agent команда проходит через встроенный предохранитель ([dispatcher](agent/internal/dispatcher/dispatcher.go)); при срабатывании выполнение отклоняется, а в сессионный аудит-лог (`.doops-audit-log`) пишется запись с пометками `blocked=1` и ID правила. Встроенные правила покрывают: рекурсивное удаление корневого и системных каталогов, запись `dd` на блочные устройства, `mkfs` и инструменты разметки разделов, выключение и перезагрузку, fork-бомбы, рекурсивную смену прав на корне, перезапись ключевых файлов `/etc`, скачивание и немедленное выполнение удалённых скриптов, уничтожение всех процессов. Перед сопоставлением выполняются сжатие пробельных символов и приведение к нижнему регистру, чтобы обходы вида `rm  -rf  /`, `RM -RF /` не срабатывали.

Предохранитель настраивается переменными окружения agent:

- `DOOPS_BLOCK_EXTRA`: дополнительные пользовательские регулярные выражения, разделённые точкой с запятой (применяются к нормализованной команде)
- `DOOPS_BLOCK_DISABLE`: отключение встроенных правил по ID правила, через запятую, например `net.pipe_shell`
- `DOOPS_BLOCK_OFF=1`: полное отключение предохранителя (только для аварийных случаев)

Предохранитель — лишь один слой эшелонированной защиты, а не граница безопасности: он не может покрыть все способы обфускации (кодирование, раскрытие переменных, косвенное выполнение через файлы скриптов), поэтому повседневная практика по-прежнему опирается на дисциплину skill и минимально необходимые права.

## Contributors

Основные участники проекта в порядке указания:

1. 孙勇 (Sūn Yǒng)
2. 高家祺 (Gāo Jiāqí)
3. 宋晨宇 (Sòng Chényǔ)

Приветствуется участие через Issue и Pull Request.

## License

Проект распространяется по лицензии [MIT License](LICENSE).
