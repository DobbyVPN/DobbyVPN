import app

class IpRepositoryImpl : IpRepository {
    func getHostnameIpData(hostname: String) -> IpData {
        return IpData(ip: "", city: "", country: "")
    }
    
    func getIpData() -> IpData {
        return IpData(ip: "", city: "", country: "")
    }
}
