Documentación de Infraestructura como Código (~/homelab-iac)

El objetivo de este repositorio es gestionar de forma declarativa, reproducible y segura la red remota y la configuración interna del servidor multimedia Jellyfin + qBittorrent sobre CachyOS, facilitando tanto la auditoría del entorno actual como una futura migración hacia un servidor dedicado.
1. Estructura del Proyecto
Plaintext

~/homelab-iac/
├── README.md                  # Documentación técnica del proyecto
├── terraform/                 # Aprovisionamiento y políticas de red (Tailscale)
│   ├── main.tf                # Definición declarativa de ACLs, Grants, SSH y Funnel
│   ├── .terraform.lock.hcl    # Versiones bloqueadas del proveedor tailscale
│   └── terraform.tfstate      # Estado sincronizado con la API de Tailscale (sensible, no subir a Git)
└── ansible/                   # Gestión de configuración del sistema operativo (CachyOS)
    ├── inventory.ini          # Definición del nodo objetivo (localhost)
    ├── secrets.yml            # Credenciales cifradas mediante Ansible Vault (API Key de Jellyfin)
    └── playbook.yml           # Tareas idempotentes de paquetes, kernel, permisos y scripts

2. Capa de Red y Aprovisionamiento: Terraform (terraform/)

Terraform administra la configuración de la red privada virtual Tailscale mediante su proveedor oficial (tailscale/tailscale).
¿Qué se implementó?

    Importación de estado (terraform import): Se vinculó la política de control de acceso (acl) previamente existente en Tailscale con el archivo de estado local (terraform.tfstate), permitiendo auditar diferencias antes de aplicar cambios.

    Control de acceso granular (grants y tagOwners):

        Propiedad de la etiqueta tag:jellyfin-app asignada a los administradores (autogroup:admin).

        Acceso completo entre dispositivos propios (autogroup:member hacia *).

        Acceso restringido para dispositivos clientes etiquetados como tag:jellyfin-app únicamente hacia el servidor multimedia (100.119.226.126) en el puerto TCP 8096 (Jellyfin).

    Acceso remoto seguro (ssh): Habilitación de Tailscale SSH con verificación (action: check) para usuarios locales (autogroup:nonroot, root).

    Exposición pública controlada (nodeAttrs): Habilitación del atributo funnel para nodos pertenecientes a autogroup:member.

Flujo de operación diario
Fragmento de código

cd ~/homelab-iac/terraform
set -x TAILSCALE_API_KEY "tskey-api-..."   # Exportar credencial en sesión
terraform plan                              # Auditar diferencias entre código y red real
terraform apply                             # Aplicar modificaciones aprobadas

3. Capa de Configuración del Servidor: Ansible (ansible/)

Ansible garantiza de forma idempotente que el sistema operativo anfitrión mantenga los paquetes, parámetros del kernel, estructura de almacenamiento, permisos y automatizaciones necesarios para operar sin intervención manual.
Componentes implementados

    Inventario (inventory.ini): Apunta actualmente a localhost mediante conexión local (ansible_connection=local), preparado para ser reemplazado por una IP remota vía SSH al migrar a otro equipo.

    Gestión de secretos (secrets.yml): Almacena la variable jellyfin_api_key cifrada con Ansible Vault (AES256), permitiendo versionar el repositorio sin exponer credenciales en texto plano.

    Playbook principal (playbook.yml): Ejecuta 8 tareas ordenadas:

        Paquetes base (pacman): Asegura la presencia de jellyfin-server, jellyfin-web, tailscale, acl y curl.

        Ajuste del kernel (sysctl): Configura de forma persistente en /etc/sysctl.d/40-max-user-watches.conf el límite fs.inotify.max_user_watches=524288 para que Jellyfin detecte archivos nuevos en tiempo real.

        Permisos de tránsito (file): Garantiza el bit de ejecución (0711) en /home/jmmaidana y /home/jmmaidana/Descargas para permitir el acceso de lectura al usuario jellyfin.

        Estructura de 4 bibliotecas y propiedad (file): Aplica recursivamente propietario jmmaidana, grupo jellyfin y modo 2775 (activando el bit setgid para heredar el grupo en subcarpetas) sobre:

            /home/jmmaidana/DatosHDD/Peliculas

            /home/jmmaidana/DatosHDD/Series

            /home/jmmaidana/Descargas/Descargas Torrent/Peliculas

            /home/jmmaidana/Descargas/Descargas Torrent/Series

        Listas de Control de Acceso (acl): Aplica permisos explícitos (rwx) actuales y reglas por defecto (default: true) para el usuario jellyfin en las 4 rutas, garantizando que toda descarga nueva sea legible y editable automáticamente.

        Automatización post-descarga (copy): Despliega el script ~/.local/bin/jellyfin-scan.sh (invocado por qBittorrent al completar cada torrent mediante "%F"), que corrige permisos sobre la descarga recién terminada y dispara una actualización de bibliotecas vía API (POST /Library/Refresh).

        Servicios del sistema (systemd): Verifica que los demonios jellyfin y tailscaled estén habilitados en el arranque y en ejecución.

Flujo de operación diario
Fragmento de código

cd ~/homelab-iac/ansible

# Auditoría en modo simulación sin modificar el sistema (Dry-run)
ansible-playbook -i inventory.ini playbook.yml --check --diff --ask-become-pass --ask-vault-pass

# Ejecución real y validación de idempotencia
ansible-playbook -i inventory.ini playbook.yml --diff --ask-become-pass --ask-vault-pass

4. Archivo .gitignore recomendado

Si decidís respaldar ~/homelab-iac en un repositorio Git, creá un archivo ~/homelab-iac/.gitignore para evitar subir archivos de estado locales o binarios pesados de proveedores:
Fragmento de código

# Archivos locales y estado sensible de Terraform
terraform/.terraform/
terraform/*.tfstate
terraform/*.tfstate.backup
terraform/.terraform.lock.hcl

# Archivos temporales de Ansible
*.retry

(Nota: ansible/secrets.yml sí puede subirse a Git de forma segura porque está cifrado con Ansible Vault).
