#!/usr/bin/env bash
# Use the existing deployment's compose file, project name and application image.
set -Eeuo pipefail
umask 077
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.yml"
ENV_FILE="${SCRIPT_DIR}/.env"
PROJECT_NAME=""
WORKER_ONLY=false
while (($#)); do
  case "$1" in
    --compose-file) COMPOSE_FILE="$2"; shift 2 ;;
    --env-file) ENV_FILE="$2"; shift 2 ;;
    --project-name) PROJECT_NAME="$2"; shift 2 ;;
    --worker-only) WORKER_ONLY=true; shift ;;
    --help) echo "Usage: sudo bash deploy/install-model-benchmark.sh [--compose-file FILE] [--env-file FILE] [--project-name NAME] [--worker-only]"; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done
[[ "$EUID" == 0 ]] || { echo 'Run as root or with sudo to install the execution environment.' >&2; exit 1; }
[[ -f "$COMPOSE_FILE" && -f "$ENV_FILE" ]] || { echo 'Provide the existing deployment compose file and .env; neither will be replaced.' >&2; exit 1; }
[[ -f "$SCRIPT_DIR/../tools/model-benchmark/Dockerfile" ]] || { echo 'Run this installer from the complete application checkout.' >&2; exit 1; }
COMPOSE_FILE="$(realpath "$COMPOSE_FILE")"
ENV_FILE="$(realpath "$ENV_FILE")"
trap 'echo "Installation stopped. Fix the cause and rerun; existing test data is retained." >&2' ERR

install_host_dependencies() {
  [[ -f /etc/os-release ]] || { echo 'Cannot determine Linux distribution.' >&2; return 1; }
  . /etc/os-release
  case "$ID" in ubuntu|debian) ;; *) echo 'Automatic Docker installation supports Ubuntu/Debian. Install Docker Engine and Compose v2 on this distribution, then rerun.' >&2; return 1 ;; esac
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends ca-certificates curl gnupg python3 util-linux
  if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
    install -m 0755 -d /etc/apt/keyrings
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time 120 \
      "https://download.docker.com/linux/${ID}/gpg" -o /etc/apt/keyrings/codebot-docker.asc
    chmod 0644 /etc/apt/keyrings/codebot-docker.asc
    printf 'deb [arch=%s signed-by=/etc/apt/keyrings/codebot-docker.asc] https://download.docker.com/linux/%s %s stable\n' \
      "$(dpkg --print-architecture)" "$ID" "${VERSION_CODENAME:?Missing distro codename}" > /etc/apt/sources.list.d/codebot-docker.list
    apt-get update
    if command -v docker >/dev/null; then
      apt-get install -y docker-compose-plugin docker-buildx-plugin
    else
      apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    fi
  fi
}
if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1 || ! command -v python3 >/dev/null || ! command -v flock >/dev/null; then
  install_host_dependencies
fi
exec 9>/run/lock/sub2api-benchmark-install.lock
flock -n 9 || { echo 'Another benchmark installer is already running.' >&2; exit 1; }
if ! docker info >/dev/null 2>&1; then
  command -v systemctl >/dev/null || { echo 'Start Docker Engine before rerunning on a host without systemd.' >&2; exit 1; }
  systemctl enable --now docker
fi
docker info >/dev/null
docker compose version >/dev/null

# Parse benchmark values as data. Never source a deployment .env.
python3 - "$ENV_FILE" "$(docker info --format '{{.DockerRootDir}}')" <<'PY'
import os,pathlib,re,secrets,sys,tempfile
p=pathlib.Path(sys.argv[1]); content=p.read_text()
if content and not content.endswith('\n'): content+='\n'
def value(key):
    values=re.findall(r'^[ \t]*(?:export[ \t]+)?'+re.escape(key)+r'[ \t]*=[ \t]*([^\r\n]*)$',content,re.M)
    if len(values)>1: raise SystemExit('Duplicate benchmark environment setting: '+key)
    if not values: return None
    raw=values[0]
    if raw[:1] in ('"', "'"):
        if not raw.endswith(raw[0]): raise SystemExit('Invalid quoted benchmark setting')
        return raw[1:-1]
    return raw.split(' #',1)[0].strip()
token=value('MODEL_BENCHMARK_WORKER_TOKEN')
if token is None:
    token=secrets.token_hex(32); content+='\nMODEL_BENCHMARK_WORKER_TOKEN='+token+'\n'
if not re.fullmatch(r'[A-Za-z0-9_-]{32,256}',token): raise SystemExit('Set MODEL_BENCHMARK_WORKER_TOKEN to 32–256 letters, digits, underscores or hyphens')
home=value('MODEL_BENCHMARK_HOME')
if home is None:
    home='/opt/sub2api-model-benchmark'; content+='MODEL_BENCHMARK_HOME='+home+'\n'
if not home.startswith('/') or home=='/' or any(c in home for c in '\r\n:$'):
    raise SystemExit('MODEL_BENCHMARK_HOME must be a dedicated absolute Linux path')
docker_root=value('MODEL_BENCHMARK_DOCKER_ROOT')
if docker_root is None:
    docker_root=sys.argv[2].strip()
    content+='MODEL_BENCHMARK_DOCKER_ROOT='+docker_root+'\n'
if not docker_root.startswith('/') or docker_root=='/' or any(c in docker_root for c in '\r\n:$'):
    raise SystemExit('MODEL_BENCHMARK_DOCKER_ROOT must be the absolute Docker data directory')
directory=pathlib.Path(home).resolve()
if str(directory) in ('/','/opt','/etc','/usr','/var','/home','/root','/run','/tmp'):
    raise SystemExit('MODEL_BENCHMARK_HOME must not be a system directory')
directory.mkdir(parents=True,exist_ok=True); directory.chmod(0o700)
fd,tmp=tempfile.mkstemp(prefix='.benchmark-env-',dir=p.parent)
try:
    with os.fdopen(fd,'w') as out: out.write(content)
    os.chown(tmp,p.stat().st_uid,p.stat().st_gid); os.chmod(tmp,0o600); os.replace(tmp,p)
finally:
    if os.path.exists(tmp): os.unlink(tmp)
PY

# Absolute build context preserves the original deployment project and volumes.
OVERLAY="$(mktemp --suffix=.yml)"
trap 'rm -f -- "$OVERLAY"' EXIT
python3 - "$SCRIPT_DIR/docker-compose.benchmark.yml" "$OVERLAY" "$SCRIPT_DIR/../tools/model-benchmark" <<'PY'
import json,pathlib,sys
text=pathlib.Path(sys.argv[1]).read_text()
text=text.replace('context: ../tools/model-benchmark','context: '+json.dumps(str(pathlib.Path(sys.argv[3]).resolve())))
pathlib.Path(sys.argv[2]).write_text(text)
PY
args=(--project-directory "$(dirname "$COMPOSE_FILE")" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" -f "$OVERLAY")
[[ -z "$PROJECT_NAME" ]] || args+=(--project-name "$PROJECT_NAME")
docker compose "${args[@]}" config --quiet
echo 'Building the worker; Python dependencies are installed into its image.'
docker compose "${args[@]}" build model-benchmark
docker compose "${args[@]}" up -d --no-deps model-benchmark
if [[ "$WORKER_ONLY" != true ]]; then
  docker compose "${args[@]}" up -d --no-deps sub2api
fi
echo 'Worker started. The question bank and sandbox image prepare automatically in the background.'
echo 'Open Model identification management > Dilution test to see progress and retry status.'
