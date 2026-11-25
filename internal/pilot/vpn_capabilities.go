package pilot

import "os/exec"

// Detection only: never install packages or modify the router from this API.
// A present binary is not proof that the kernel supports its tunnel type.
func independentVPNCapabilities() map[string]bool {
 result := make(map[string]bool)
 for _, name := range []string{"ssh", "sshpass", "wg", "awg", "wireguard-go", "amneziawg-go", "openvpn", "ip"} {
  _, err := exec.LookPath(name)
  result[name] = err == nil
 }
 return result
}
