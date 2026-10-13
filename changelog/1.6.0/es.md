# SPK Ocular 1.6.0

## Comandos de agentes en contenedores

- Los agentes locales descubren instancias y preparan/ejecutan comandos finitos con `GetExecInfo`, `PrepareExec` y `RunExec`. stdout/stderr separados se devuelven en base64, con estado de salida observado y límites de salida y tiempo.
- El permiso independiente `exec` exige tipos concretos y selección explícita de instancia/contenedor. Cada comando requiere confirmación por defecto con argv, stdin finito, destino y límites exactos. No hay shell interactivo persistente, shell implícito, attach ni repetición automática.
- Se conservan los límites de namespaces Kubernetes y proyectos Docker Compose, identidad, UID y revisión de configuración. Los cambios de permisos cancelan el transporte; detener el proceso remoto es best effort. Los resultados incompletos o desconocidos son explícitos. Los contenedores Docker independientes no heredan permisos de proyectos.
- Exec usa los permisos del contenedor y puede leer secretos montados o modificar datos del contenedor/host. Comandos, entrada y salida no se guardan en el registro de auditoría. Los permisos existentes no adquieren exec automáticamente.

## Correcciones

- La confirmación de eliminación de descargas enfoca Cancelar al terminar la preparación.
- La entrada finita de Docker envía EOF conservando stdout/stderr; los comandos sin entrada desconectan stdin. Los transportes incompatibles se rechazan antes del inicio.
- Las confirmaciones de agentes en alemán muestran texto correctamente codificado. Los controles y confirmaciones de exec están traducidos a los ocho idiomas.

Los paquetes conservan el ejecutable unificado de escritorio/navegador y la verificación en las seis combinaciones nativas de sistema y arquitectura.
