# SPK Ocular 1.6.2

- Al cambiar la altura del panel de registros, las líneas visibles conservan su posición en pantalla. Follow permanece en la última línea; leer el historial no vuelve a activar Follow. Reducir, ampliar, cancelar el cambio de tamaño y volver a una pestaña oculta conservan la posición dentro del intervalo de desplazamiento disponible.
- Activar o desactivar Wrap mantiene visible la última línea cuando Follow está activo, incluso si Wrap cambia durante el ajuste del tamaño.
- Nueva configuración desbloquea directamente el almacenamiento local cifrado con la contraseña maestra y abre el editor YAML, sin requerir Connect ni iniciar una conexión al clúster. También funciona si ya existe una contraseña maestra pero todavía no se ha guardado ninguna configuración cifrada, o se ha eliminado la última.
- Cancelar la introducción de la contraseña no modifica el almacenamiento. Connect sigue desbloqueando y continuando únicamente la conexión seleccionada explícitamente. La explicación para crear una configuración está disponible en los ocho idiomas de la aplicación.
