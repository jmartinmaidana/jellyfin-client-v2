# Homelab Media Stack: Jellyfin + Tailscale + Direct Play Desktop Client

Solución integral de infraestructura como código (IaC) y cliente de escritorio nativo para desplegar un servidor multimedia **Jellyfin** sobre **Arch Linux / CachyOS**, asegurado mediante una red privada **Tailscale** con control de acceso granular (ACLs) y acompañado por un cliente portátil para **Windows** escrito en **Go** que fuerza reproducción directa (**Direct Play**) mediante **mpv** sin transcodificación en el servidor.

---

## Arquitectura del Sistema

El ecosistema se divide en tres capas desacopladas:

1. **Red Privada y Control de Acceso (`terraform/`):**
   * Define las políticas ACL de Tailscale.
   * Restringe los dispositivos cliente (`tag:jellyfin-app`) para que accedan únicamente al puerto TCP de Jellyfin (`8096`) en la IP privada del servidor, bloqueando el acceso SSH o a otros servicios de la red.
   * Genera automáticamente una `AuthKey` reutilizable y preautorizada para los clientes.

2. **Aprovisionamiento del Servidor (`ansible/`):**
   * Instala y habilita `jellyfin-server`, `jellyfin-web`, `tailscale`, `acl` y `curl`.
   * Ajusta `fs.inotify.max_user_watches` para detección instantánea de nuevos archivos multimedia.
   * Configura permisos POSIX (`0711` en directorios padre, bit `setgid` `2775` en bibliotecas) y listas de control de acceso (ACLs `rwx` por defecto) para evitar conflictos de permisos entre el usuario propietario, clientes torrent y el servicio `jellyfin`.
   * Despliega el hook `~/.local/bin/jellyfin-scan.sh` para disparar escaneos de biblioteca vía API al finalizar descargas.

3. **Cliente Portátil de Escritorio (`jellyfin-client-v2/`):**
   * **Detección híbrida de red:** Evalúa en paralelo las interfaces locales (Ethernet / Wi-Fi) en menos de 220 ms; si el servidor está en la LAN, conecta directo a velocidad local; si está fuera de casa, despliega e inicia Tailscale de forma silenciosa y enruta el tráfico por el túnel cifrado.
   * **Proxy inverso + Interfaz WebView2:** Renderiza la interfaz web oficial de Jellyfin e intercepta las peticiones de reproducción (`/videos/.../master.m3u8` o `stream`) para delegarlas localmente al reproductor MPV.
   * **Reproducción nativa con `mpv`:** Lanza `mpv.exe` en modo *Direct Play* con sincronización bidireccional de progreso hacia la API de Jellyfin (`Sessions/Playing/Progress`).

---

## Requisitos Previos

* **Servidor:** Distribución basada en Arch Linux (CachyOS / Arch) con acceso `sudo`.
* **Cuenta de Tailscale:** Plan gratuito con MagicDNS habilitado y un *API Access Token* (`tskey-api-...`) generado desde *Settings -> Keys*.
* **Entorno de compilación (para generar el cliente Windows desde Linux):**
  * `go` (>= 1.22), `mingw-w64-gcc`, `zip`, `curl`, `7zip` (`p7zip`) y `tar` con soporte `zstd`.
  * En Arch / CachyOS:
    ```bash
    sudo pacman -S go mingw-w64-gcc zip curl 7zip terraform ansible
    ```

---

## Guía de Despliegue Paso a Paso

### Paso 1: Configurar la Red Privada con Terraform

1. Ingresá al directorio `terraform/` y creá tu archivo de variables a partir de la plantilla:
   ```bash
   cd homelab-iac/terraform
   cp terraform.tfvars.example terraform.tfvars
   ```
2. Editá `terraform.tfvars` con la IP fija de Tailscale asignada a tu servidor:
   ```hcl
   jellyfin_server_tailscale_ip = "100.x.x.x"
   client_tag                   = "tag:jellyfin-app"
   jellyfin_port                = "8096"
   ```
3. Exportá tu token de API de Tailscale y aplicá la infraestructura:
   ```bash
   export TAILSCALE_API_KEY="tskey-api-TU_TOKEN"
   terraform init
   terraform apply
   ```
4. Obtené la clave generada automáticamente para los clientes (la necesitarás en el Paso 3):
   ```bash
   terraform output -raw client_auth_key
   ```

---

### Paso 2: Aprovisionar el Servidor con Ansible

1. Ingresá al directorio `ansible/` y copiá las plantillas de inventario y variables:
   ```bash
   cd ../ansible
   cp inventory.ini.example inventory.ini
   cp group_vars/all.yml.example group_vars/all.yml
   cp secrets.yml.example secrets.yml
   ```
2. Editá `group_vars/all.yml` con tu usuario del sistema, puerto y rutas absolutas de tus carpetas multimedia.
3. Generá una API Key en el panel de administración de Jellyfin (*Panel -> Claves de API*), pegala en `secrets.yml` y cifrá el archivo con Ansible Vault:
   ```bash
   ansible-vault encrypt secrets.yml
   ```
4. Ejecutá el playbook para configurar el servidor de forma idempotente:
   ```bash
   ansible-playbook -i inventory.ini playbook.yml --ask-become-pass --ask-vault-pass
   ```

---

### Paso 3: Compilar y Empaquetar el Cliente para Windows

1. Ingresá al directorio `jellyfin-client-v2/` y creá tu configuración local:
   ```bash
   cd ../../jellyfin-client-v2
   cp .env.example .env
   ```
2. Editá `.env` completando la `TAILSCALE_AUTH_KEY` obtenida en el Paso 1, la URL remota de Tailscale y tus IPs locales (Ethernet/Wi-Fi):
   ```env
   TAILSCALE_AUTH_KEY="tskey-auth-TU_CLAVE"
   REMOTE_TARGET_URL="http://100.x.x.x:8096"
   LOCAL_HOSTS="192.168.1.37:8096,192.168.1.5:8096"
   ```
3. Ejecutá el script de compilación y empaquetado automatizado:
   ```bash
   chmod +x build.sh
   ./build.sh
   ```
   El script compilará `JellyfinCliente.exe` inyectando las variables mediante `-ldflags`, descargará automáticamente las dependencias portables en `dist/` (`MicrosoftEdgeWebView2Setup.exe`, `tailscale-setup-amd64.msi`, `mpv.exe` y `vulkan-1.dll`) y generará el archivo listo para distribuir: **`JellyfinCliente-Windows-v2.zip`**.

---

## Uso en Windows (Usuario Final)

1. Descomprimir `JellyfinCliente-Windows-v2.zip` en cualquier carpeta de la PC con Windows 10/11.
2. Ejecutar **`JellyfinCliente.exe`** (la primera vez que se utilice fuera de la red local solicitará permisos de Administrador para instalar y vincular el túnel privado de Tailscale automáticamente). (En algunos casos será necesario desactivar la protección en tiempo real de windows defender)
3. **Opciones de línea de comandos para diagnóstico:**
   * `JellyfinCliente.exe --remote`: Fuerza la conexión a través del túnel de Tailscale incluso estando dentro de la red local.
   * `JellyfinCliente.exe --debug`: Habilita el registro detallado de eventos de red y reproducción.